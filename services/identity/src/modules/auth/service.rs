use chrono::{Duration as ChronoDuration, Utc};
use regex::Regex;
use sha2::{Digest, Sha256};
use std::sync::OnceLock;
use uuid::Uuid;

use crate::error::{AppError, AppResult};
use crate::modules::credential::repo::CredentialRepo;
use crate::modules::token::repo::TokenRepo;
use crate::modules::user::repo::{NewPolicyAcceptance, UserRepo};
use crate::state::AppState;

use super::dto::RegisterRequest;

const NEUTRAL_REGISTER_MSG: &str =
    "If the information is valid, a verification email will be sent.";
const POLICY_VERSION: &str = "v1";
const POLICY_TERMS_SHA256: &str =
    "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855";
const POLICY_PRIVACY_SHA256: &str =
    "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855";
const EMAIL_VERIFY_TTL_HOURS: i64 = 24;
const NEUTRAL_RESEND_MSG: &str = "If an account is pending verification, a new email will be sent.";

fn username_re() -> &'static Regex {
    static RE: OnceLock<Regex> = OnceLock::new();
    RE.get_or_init(|| Regex::new(r"^[a-z0-9_.]{3,30}$").expect("username regex"))
}

fn normalize_email(email: &str) -> String {
    email.trim().to_ascii_lowercase()
}

fn normalize_username(username: &str) -> String {
    username.trim().to_ascii_lowercase()
}

pub async fn register(state: &AppState, ip: &str, body: RegisterRequest) -> AppResult<String> {
    if !body.accept_policy {
        return Err(AppError::BadRequest("accept_policy must be true".into()));
    }

    let email = normalize_email(&body.email);
    let username = normalize_username(&body.username);

    if !username_re().is_match(&username) {
        return Err(AppError::BadRequest("invalid username".into()));
    }

    state
        .rate_limiter
        .check_and_incr("register", "ip", ip, 10, 3600)
        .await?;
    state
        .rate_limiter
        .check_and_incr("register", "email", &email, 5, 3600)
        .await?;

    let password = body.password.clone();
    let policy = state.password_policy.clone();
    let password_hash = tokio::task::spawn_blocking(move || policy.hash(&password))
        .await
        .map_err(|_| AppError::Internal)??;

    let user_id = Uuid::now_v7();
    let email_id = Uuid::now_v7();
    let username_id = Uuid::now_v7();
    let history_id = Uuid::now_v7();
    let terms_id = Uuid::now_v7();
    let privacy_id = Uuid::now_v7();
    let token_id = Uuid::now_v7();
    let expires_at = Utc::now() + ChronoDuration::hours(EMAIL_VERIFY_TTL_HOURS);

    let mut tx = state.db.begin().await.map_err(|e| {
        tracing::error!(error = %e, "begin register transaction failed");
        AppError::Internal
    })?;

    let insert_result = async {
        UserRepo::insert_pending_tx(
            &mut tx,
            user_id,
            body.locale.as_deref(),
            body.timezone.as_deref(),
        )
        .await?;

        UserRepo::insert_profile_tx(&mut tx, user_id, body.full_name.as_deref(), Some(&username))
            .await?;

        UserRepo::insert_email_tx(&mut tx, email_id, user_id, &email, true).await?;
        UserRepo::insert_username_tx(&mut tx, username_id, user_id, &username).await?;

        CredentialRepo::insert_password_tx(&mut tx, user_id, &password_hash).await?;
        CredentialRepo::insert_history_tx(&mut tx, history_id, user_id, &password_hash).await?;

        UserRepo::insert_policy_acceptance_tx(
            &mut tx,
            NewPolicyAcceptance {
                id: terms_id,
                user_id,
                policy_type: "terms",
                version: POLICY_VERSION,
                document_sha256: POLICY_TERMS_SHA256,
                ip: Some(ip),
                user_agent: None,
            },
        )
        .await?;

        UserRepo::insert_policy_acceptance_tx(
            &mut tx,
            NewPolicyAcceptance {
                id: privacy_id,
                user_id,
                policy_type: "privacy",
                version: POLICY_VERSION,
                document_sha256: POLICY_PRIVACY_SHA256,
                ip: Some(ip),
                user_agent: None,
            },
        )
        .await?;

        TokenRepo::insert_tx(
            &mut tx,
            token_id,
            user_id,
            "email_verify",
            &email,
            expires_at,
        )
        .await?;

        Ok::<(), AppError>(())
    }
    .await;

    match insert_result {
        Ok(()) => {
            tx.commit().await.map_err(|e| {
                tracing::error!(error = %e, "commit register transaction failed");
                AppError::Internal
            })?;
            Ok(NEUTRAL_REGISTER_MSG.to_string())
        }
        Err(AppError::Conflict) => {
            let _ = tx.rollback().await;
            handle_register_conflict(state, ip, &email).await
        }
        Err(e) => {
            let _ = tx.rollback().await;
            Err(e)
        }
    }
}

async fn handle_register_conflict(state: &AppState, _ip: &str, email: &str) -> AppResult<String> {
    let user_repo = UserRepo::new(&state.db);
    if let Some((user, email_row)) = user_repo.find_by_email(email).await? {
        if user.status == "pending" {
            let mut tx = state.db.begin().await.map_err(|e| {
                tracing::error!(error = %e, "begin pending reissue tx failed");
                AppError::Internal
            })?;

            TokenRepo::revoke_active_for_user_purpose_tx(&mut tx, user.id, "email_verify").await?;

            let token_id = Uuid::now_v7();
            let expires_at = Utc::now() + ChronoDuration::hours(EMAIL_VERIFY_TTL_HOURS);
            TokenRepo::insert_tx(
                &mut tx,
                token_id,
                user.id,
                "email_verify",
                &email_row.email,
                expires_at,
            )
            .await?;

            tx.commit().await.map_err(|e| {
                tracing::error!(error = %e, "commit pending reissue failed");
                AppError::Internal
            })?;
        }
    }

    Ok(NEUTRAL_REGISTER_MSG.to_string())
}

pub async fn verify_email(state: &AppState, ip: &str, token: &str) -> AppResult<String> {
    state
        .rate_limiter
        .check_and_incr("verify_email", "ip", ip, 30, 600)
        .await?;

    let token_id = state
        .magic_link_signer
        .verify("email_verify", token)
        .map_err(|_| AppError::BadRequest("invalid token".into()))?;

    let mut tx = state.db.begin().await.map_err(|e| {
        tracing::error!(error = %e, "begin verify-email transaction failed");
        AppError::Internal
    })?;

    let consumed = TokenRepo::consume_tx(&mut tx, token_id, "email_verify")
        .await?
        .ok_or_else(|| AppError::BadRequest("invalid token".into()))?;

    UserRepo::activate_verified_email_tx(&mut tx, consumed.user_id, &consumed.email).await?;

    tx.commit().await.map_err(|e| {
        tracing::error!(error = %e, "commit verify-email transaction failed");
        AppError::Internal
    })?;

    Ok("Email verified.".to_string())
}

#[allow(dead_code)]
fn hash_document(content: &str) -> String {
    let mut hasher = Sha256::new();
    hasher.update(content.as_bytes());
    hex::encode(hasher.finalize())
}

pub async fn resend_verification(state: &AppState, ip: &str, email_raw: &str) -> AppResult<String> {
    let email = normalize_email(email_raw);

    state
        .rate_limiter
        .check_and_incr("resend_verification", "ip", ip, 10, 3600)
        .await?;
    state
        .rate_limiter
        .check_and_incr("resend_verification", "email", &email, 3, 3600)
        .await?;

    let user_repo = UserRepo::new(&state.db);
    let Some((user, email_row)) = user_repo.find_by_email(&email).await? else {
        return Ok(NEUTRAL_RESEND_MSG.to_string());
    };

    if user.status != "pending" {
        return Ok(NEUTRAL_RESEND_MSG.to_string());
    }

    let mut tx = state.db.begin().await.map_err(|e| {
        tracing::error!(error = %e, "begin resend-verification transaction failed");
        AppError::Internal
    })?;

    TokenRepo::revoke_active_for_user_purpose_tx(&mut tx, user.id, "email_verify").await?;

    let token_id = Uuid::now_v7();
    let expires_at = Utc::now() + ChronoDuration::hours(EMAIL_VERIFY_TTL_HOURS);
    TokenRepo::insert_tx(
        &mut tx,
        token_id,
        user.id,
        "email_verify",
        &email_row.email,
        expires_at,
    )
    .await?;

    tx.commit().await.map_err(|e| {
        tracing::error!(error = %e, "commit resend-verification failed");
        AppError::Internal
    })?;

    Ok(NEUTRAL_RESEND_MSG.to_string())
}
