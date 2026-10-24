use chrono::{Duration as ChronoDuration, Utc};
use regex::Regex;
use sha2::{Digest, Sha256};
use std::sync::OnceLock;
use uuid::Uuid;

use crate::error::{AppError, AppResult};

use crate::infra::refresh_token::{generate_opaque_token, hash_token};
use crate::modules::credential::repo::CredentialRepo;
use crate::modules::session::repo::{RefreshTokenRepo, SessionRepo};
use crate::modules::token::repo::TokenRepo;
use crate::modules::user::repo::{NewPolicyAcceptance, UserRepo};
use crate::state::AppState;

/// Chờ suất xử lý Argon2 rồi chạy tác vụ chặn, trả kết quả hoặc lỗi của tác vụ.
///
/// Trả `Internal` nếu semaphore đã đóng hoặc tác vụ không hoàn tất bình thường.
async fn with_argon_permit<T, F>(state: &AppState, f: F) -> AppResult<T>
where
    F: FnOnce() -> AppResult<T> + Send + 'static,
    T: Send + 'static,
{
    let permit = state
        .argon_permits
        .clone()
        .acquire_owned()
        .await
        .map_err(|_| AppError::Internal)?;
    tokio::task::spawn_blocking(move || {
        let _permit = permit;
        f()
    })
    .await
    .map_err(|_| AppError::Internal)?
}

use super::dto::{LoginRequest, LoginResponse, RegisterRequest};

const NEUTRAL_REGISTER_MSG: &str =
    "If the information is valid, a verification email will be sent.";
const POLICY_VERSION: &str = "v1";
const POLICY_TERMS_SHA256: &str =
    "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855";
const POLICY_PRIVACY_SHA256: &str =
    "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855";
const EMAIL_VERIFY_TTL_HOURS: i64 = 24;
const NEUTRAL_RESEND_MSG: &str = "If an account is pending verification, a new email will be sent.";
const NEUTRAL_FORGOT_MSG: &str =
    "If an account with that email exists, a password reset link will be sent.";
const PASSWORD_RESET_TTL_HOURS: i64 = 1;
const PASSWORD_HISTORY_LIMIT: i64 = 5;
const RESTORE_TTL_HOURS: i64 = 48;
const PURGE_AFTER_DAYS: i64 = 30;
const NEUTRAL_RESTORE_MSG: &str =
    "If a deleted account with that email exists and is within the restore window, a restore link will be sent.";

/// Trả mẫu tên đăng nhập gồm 3–30 ký tự ASCII: chữ thường, chữ số, `_` hoặc `.`.
fn username_re() -> &'static Regex {
    static RE: OnceLock<Regex> = OnceLock::new();
    RE.get_or_init(|| Regex::new(r"^[a-z0-9_.]{3,30}$").expect("username regex"))
}

/// Bỏ khoảng trắng hai đầu email và chuyển chữ ASCII sang chữ thường.
fn normalize_email(email: &str) -> String {
    email.trim().to_ascii_lowercase()
}

/// Bỏ khoảng trắng hai đầu tên đăng nhập và chuyển chữ ASCII sang chữ thường.
fn normalize_username(username: &str) -> String {
    username.trim().to_ascii_lowercase()
}

/// Chọn cách tìm theo email khi định danh chứa `@`; không xác thực cú pháp email.
fn looks_like_email(s: &str) -> bool {
    s.contains('@')
}

/// Tạo tài khoản pending cùng thông tin đăng ký và token xác minh email hạn 24 giờ.
///
/// Khi trùng dữ liệu, trả cùng thông báo trung tính; nếu email thuộc tài khoản
/// pending thì thay token xác minh cũ. Email được xếp hàng để worker gửi.
/// Trả `BadRequest` khi chưa chấp nhận chính sách, username hoặc mật khẩu không
/// hợp lệ; truyền lỗi `TooManyRequests`/`Unavailable` từ giới hạn IP và email,
/// cùng `Internal` từ xử lý mật khẩu, database hoặc giao dịch.
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
    let password_hash = with_argon_permit(state, move || policy.hash(&password)).await?;

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

/// Xếp lại email xác minh hạn 24 giờ cho tài khoản pending có email tương ứng,
/// thu hồi token xác minh cũ và trả thông báo đăng ký trung tính cho mọi trường hợp.
/// Lỗi truy vấn hoặc giao dịch vẫn được truyền lên dưới dạng `Internal`.
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

/// Tiêu thụ token email_verify còn hạn và kích hoạt tài khoản pending
/// cùng email chưa xác minh trong một giao dịch; trả thông báo xác minh thành công.
/// Trả `BadRequest` khi token hoặc trạng thái tài khoản/email không hợp lệ;
/// truyền lỗi `TooManyRequests`, `Unavailable` và `Internal` từ các phụ thuộc.
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

/// Trả SHA-256 dạng hex của nội dung tài liệu.
#[allow(dead_code)]
fn hash_document(content: &str) -> String {
    let mut hasher = Sha256::new();
    hasher.update(content.as_bytes());
    hex::encode(hasher.finalize())
}

/// Thay token xác minh cũ bằng token hạn 24 giờ và xếp email cho tài khoản pending.
///
/// Trả cùng thông báo trung tính khi email không tồn tại hoặc không còn pending.
/// Truyền lỗi `TooManyRequests`/`Unavailable` từ giới hạn IP và email, cùng
/// `Internal` từ database hoặc giao dịch.
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

/// Đăng nhập bằng email hoặc username, tạo phiên và trả access token, refresh token.
///
/// Chỉ tài khoản active với mật khẩu đúng được chấp nhận; các trường hợp khác
/// trả `Unauthorized`. Refresh token hết hạn không muộn hơn phiên, `expires_in`
/// là TTL access token tính bằng giây. IP, user-agent và tên thiết bị được lưu
/// với phiên. Phiên được commit trước khi ký JWT nên lỗi ký có thể xảy ra sau đó.
/// Truyền lỗi `TooManyRequests`, `Unavailable` và `Internal` từ các phụ thuộc;
/// lỗi ghi sự kiện đăng nhập thất bại được bỏ qua.
pub async fn login(
    state: &AppState,
    ip: &str,
    user_agent: Option<&str>,
    body: LoginRequest,
) -> AppResult<LoginResponse> {
    let login = body.login.trim().to_ascii_lowercase();
    let password = body.password.clone();

    state
        .rate_limiter
        .check_and_incr("login", "ip", ip, 20, 900)
        .await?;
    state
        .rate_limiter
        .check_and_incr("login", "id", &login, 10, 900)
        .await?;

    let user_repo = UserRepo::new(&state.db);

    let user = if looks_like_email(&login) {
        user_repo.find_by_email(&login).await?.map(|(u, _)| u)
    } else {
        user_repo.find_by_username(&login).await?
    };

    let Some(user) = user else {
        let policy = state.password_policy.clone();
        let _ = with_argon_permit(state, move || {
            policy.dummy_verify();
            Ok(())
        })
        .await;
        insert_security_event(state, "login_failed", None, None, Some(ip), user_agent).await;
        return Err(AppError::Unauthorized);
    };

    if user.status != "active" {
        let policy = state.password_policy.clone();
        let _ = with_argon_permit(state, move || {
            policy.dummy_verify();
            Ok(())
        })
        .await;
        insert_security_event(
            state,
            "login_failed",
            Some(user.id),
            None,
            Some(ip),
            user_agent,
        )
        .await;
        return Err(AppError::Unauthorized);
    }

    let hash = CredentialRepo::find_password_hash(&state.db, user.id)
        .await?
        .ok_or(AppError::Unauthorized)?;

    let policy = state.password_policy.clone();
    let password_ok = with_argon_permit(state, move || policy.verify(&password, &hash)).await?;

    if !password_ok {
        insert_security_event(
            state,
            "login_failed",
            Some(user.id),
            None,
            Some(ip),
            user_agent,
        )
        .await;
        return Err(AppError::Unauthorized);
    }

    let session_id = Uuid::now_v7();
    let refresh_id = Uuid::now_v7();
    let now = Utc::now();
    let session_expires = now + ChronoDuration::seconds(state.config.session_ttl_secs);
    let refresh_expires = now + ChronoDuration::seconds(state.config.refresh_ttl_secs);
    let refresh_expires = if refresh_expires > session_expires {
        session_expires
    } else {
        refresh_expires
    };

    let raw_refresh = generate_opaque_token();
    let token_hash = hash_token(&raw_refresh);

    let mut tx = state.db.begin().await.map_err(|e| {
        tracing::error!(error = %e, "begin login transaction failed");
        AppError::Internal
    })?;

    SessionRepo::insert_tx(
        &mut tx,
        session_id,
        user.id,
        Some(ip),
        user_agent,
        body.device_name.as_deref(),
        session_expires,
    )
    .await?;

    RefreshTokenRepo::insert_tx(
        &mut tx,
        refresh_id,
        session_id,
        None,
        &token_hash,
        refresh_expires,
    )
    .await?;

    insert_security_event_tx(
        &mut tx,
        "login_success",
        Some(user.id),
        Some(session_id),
        Some(ip),
        user_agent,
    )
    .await?;

    tx.commit().await.map_err(|e| {
        tracing::error!(error = %e, "commit login transaction failed");
        AppError::Internal
    })?;

    let access_token = state.jwt_signer.issue_access_token(user.id, session_id)?;

    Ok(LoginResponse {
        access_token,
        refresh_token: raw_refresh,
        token_type: "Bearer".to_string(),
        expires_in: state.jwt_signer.access_ttl_secs(),
    })
}

/// Thu hồi phiên trong access token và mọi refresh token của phiên, ghi sự kiện
/// rồi trả thông báo đăng xuất. Trả `Unauthorized` khi token không hợp lệ,
/// phiên không tồn tại, sai chủ sở hữu hoặc đã thu hồi. Truyền lỗi `Internal`
/// từ bộ xác minh, truy vấn và giao dịch.
pub async fn logout(state: &AppState, access_token: &str, ip: &str) -> AppResult<String> {
    let claims = state.jwt_verifier.verify_access_token(access_token)?;

    let session_id = Uuid::parse_str(&claims.sid).map_err(|_| AppError::Unauthorized)?;
    let user_id = Uuid::parse_str(&claims.sub).map_err(|_| AppError::Unauthorized)?;

    let mut tx = state.db.begin().await.map_err(|e| {
        tracing::error!(error = %e, "begin logout transaction failed");
        AppError::Internal
    })?;

    let session = SessionRepo::find_by_id_for_update_tx(&mut tx, session_id)
        .await?
        .ok_or(AppError::Unauthorized)?;

    if session.user_id != user_id || session.revoked_at.is_some() {
        let _ = tx.rollback().await;
        return Err(AppError::Unauthorized);
    }

    SessionRepo::revoke_tx(&mut tx, session_id, "logout").await?;
    RefreshTokenRepo::revoke_all_for_session_tx(&mut tx, session_id).await?;

    insert_security_event_tx(
        &mut tx,
        "logout",
        Some(user_id),
        Some(session_id),
        Some(ip),
        None,
    )
    .await?;

    tx.commit().await.map_err(|e| {
        tracing::error!(error = %e, "commit logout transaction failed");
        AppError::Internal
    })?;

    Ok("Logged out.".to_string())
}

/// Thử lưu sự kiện bảo mật cùng IP và user-agent; bỏ qua lỗi ghi database.
async fn insert_security_event(
    state: &AppState,
    event_type: &str,
    user_id: Option<Uuid>,
    session_id: Option<Uuid>,
    ip: Option<&str>,
    user_agent: Option<&str>,
) {
    let id = Uuid::now_v7();
    let _ = sqlx::query(
        r#"
        INSERT INTO audit.security_events (id, event_type, user_id, session_id, ip, user_agent)
        VALUES ($1, $2, $3, $4, $5, $6)
        "#,
    )
    .bind(id)
    .bind(event_type)
    .bind(user_id)
    .bind(session_id)
    .bind(ip)
    .bind(user_agent)
    .execute(&state.db)
    .await;
}

/// Lưu sự kiện bảo mật trong giao dịch của bên gọi; lỗi database trả `Internal`.
async fn insert_security_event_tx(
    tx: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    event_type: &str,
    user_id: Option<Uuid>,
    session_id: Option<Uuid>,
    ip: Option<&str>,
    user_agent: Option<&str>,
) -> AppResult<()> {
    let id = Uuid::now_v7();
    sqlx::query(
        r#"
        INSERT INTO audit.security_events (id, event_type, user_id, session_id, ip, user_agent)
        VALUES ($1, $2, $3, $4, $5, $6)
        "#,
    )
    .bind(id)
    .bind(event_type)
    .bind(user_id)
    .bind(session_id)
    .bind(ip)
    .bind(user_agent)
    .execute(&mut **tx)
    .await
    .map_err(|e| {
        tracing::error!(error = %e, "insert security event failed");
        AppError::Internal
    })?;
    Ok(())
}

/// Đổi refresh token còn hạn thành cặp token mới và cập nhật lần dùng phiên.
///
/// Token cũ chỉ dùng một lần: tái sử dụng token đã dùng hoặc thu hồi sẽ thu hồi
/// cả phiên và các refresh token của phiên rồi trả `Unauthorized`. Token mới
/// không hết hạn sau phiên; `expires_in` tính bằng giây. Giao dịch được commit
/// trước khi ký access token nên lỗi ký vẫn có thể xảy ra sau khi đã xoay token.
/// Token hoặc phiên không hợp lệ trả `Unauthorized`; truyền lỗi `TooManyRequests`,
/// `Unavailable` và `Internal` từ các phụ thuộc. Lỗi commit khi xử lý phiên đã
/// hết hạn hoặc thu hồi được bỏ qua trước khi trả `Unauthorized`.
pub async fn refresh(
    state: &AppState,
    ip: &str,
    user_agent: Option<&str>,
    raw_refresh: &str,
) -> AppResult<LoginResponse> {
    state
        .rate_limiter
        .check_and_incr("refresh", "ip", ip, 60, 900)
        .await?;

    let token_hash = hash_token(raw_refresh);

    let mut tx = state.db.begin().await.map_err(|e| {
        tracing::error!(error = %e, "begin refresh transaction failed");
        AppError::Internal
    })?;

    let existing = RefreshTokenRepo::find_by_hash_for_update_tx(&mut tx, &token_hash)
        .await?
        .ok_or(AppError::Unauthorized)?;

    if existing.revoked_at.is_some() || existing.used_at.is_some() {
        let session_id = existing.session_id;
        SessionRepo::revoke_tx(&mut tx, session_id, "refresh_reuse").await?;
        RefreshTokenRepo::revoke_all_for_session_tx(&mut tx, session_id).await?;

        insert_security_event_tx(
            &mut tx,
            "refresh_reuse_detected",
            None,
            Some(session_id),
            Some(ip),
            user_agent,
        )
        .await?;

        tx.commit().await.map_err(|e| {
            tracing::error!(error = %e, "commit reuse revoke failed");
            AppError::Internal
        })?;

        return Err(AppError::Unauthorized);
    }

    if existing.expires_at <= Utc::now() {
        let _ = tx.rollback().await;
        return Err(AppError::Unauthorized);
    }

    let session = SessionRepo::find_by_id_for_update_tx(&mut tx, existing.session_id)
        .await?
        .ok_or(AppError::Unauthorized)?;

    if session.revoked_at.is_some() || session.expires_at <= Utc::now() {
        RefreshTokenRepo::revoke_all_for_session_tx(&mut tx, session.id).await?;
        let _ = tx.commit().await;
        return Err(AppError::Unauthorized);
    }

    RefreshTokenRepo::mark_used_tx(&mut tx, existing.id).await?;

    let new_refresh_id = Uuid::now_v7();
    let now = Utc::now();
    let refresh_expires = now + ChronoDuration::seconds(state.config.refresh_ttl_secs);
    let refresh_expires = if refresh_expires > session.expires_at {
        session.expires_at
    } else {
        refresh_expires
    };

    let raw_new = generate_opaque_token();
    let new_hash = hash_token(&raw_new);

    RefreshTokenRepo::insert_tx(
        &mut tx,
        new_refresh_id,
        session.id,
        Some(existing.id),
        &new_hash,
        refresh_expires,
    )
    .await?;

    SessionRepo::touch_last_seen_tx(&mut tx, session.id).await?;

    insert_security_event_tx(
        &mut tx,
        "refresh_success",
        Some(session.user_id),
        Some(session.id),
        Some(ip),
        user_agent,
    )
    .await?;

    tx.commit().await.map_err(|e| {
        tracing::error!(error = %e, "commit refresh transaction failed");
        AppError::Internal
    })?;

    let access_token = state
        .jwt_signer
        .issue_access_token(session.user_id, session.id)?;

    Ok(LoginResponse {
        access_token,
        refresh_token: raw_new,
        token_type: "Bearer".to_string(),
        expires_in: state.jwt_signer.access_ttl_secs(),
    })
}

/// Thay token đặt lại mật khẩu bằng token hạn một giờ và xếp email cho tài khoản active.
///
/// Trả cùng thông báo trung tính khi email không tồn tại hoặc tài khoản không
/// active. Truyền lỗi `TooManyRequests`/`Unavailable` từ giới hạn IP và email,
/// cùng `Internal` từ database hoặc giao dịch.
pub async fn forgot_password(state: &AppState, ip: &str, email_raw: &str) -> AppResult<String> {
    let email = normalize_email(email_raw);

    state
        .rate_limiter
        .check_and_incr("forgot_password", "ip", ip, 10, 3600)
        .await?;
    state
        .rate_limiter
        .check_and_incr("forgot_password", "email", &email, 3, 3600)
        .await?;

    let user_repo = UserRepo::new(&state.db);
    let Some((user, email_row)) = user_repo.find_by_email(&email).await? else {
        return Ok(NEUTRAL_FORGOT_MSG.to_string());
    };

    if user.status != "active" {
        return Ok(NEUTRAL_FORGOT_MSG.to_string());
    }

    let mut tx = state.db.begin().await.map_err(|e| {
        tracing::error!(error = %e, "begin forgot-password transaction failed");
        AppError::Internal
    })?;

    TokenRepo::revoke_active_for_user_purpose_tx(&mut tx, user.id, "password_reset").await?;

    let token_id = Uuid::now_v7();
    let expires_at = Utc::now() + ChronoDuration::hours(PASSWORD_RESET_TTL_HOURS);
    TokenRepo::insert_tx(
        &mut tx,
        token_id,
        user.id,
        "password_reset",
        &email_row.email,
        expires_at,
    )
    .await?;

    insert_security_event_tx(
        &mut tx,
        "password_reset_requested",
        Some(user.id),
        None,
        Some(ip),
        None,
    )
    .await?;

    tx.commit().await.map_err(|e| {
        tracing::error!(error = %e, "commit forgot-password failed");
        AppError::Internal
    })?;

    Ok(NEUTRAL_FORGOT_MSG.to_string())
}

/// Đặt mật khẩu bằng token password_reset và thu hồi mọi phiên cùng refresh token.
///
/// Tiêu thụ token, cập nhật mật khẩu và giữ tối đa năm hash lịch sử trong cùng
/// giao dịch; trả thông báo thành công. Token không hợp lệ, mật khẩu ngoài
/// 8–128 byte hoặc trùng một trong năm hash gần nhất trả `BadRequest`.
/// Truyền lỗi `TooManyRequests`, `Unavailable` và `Internal` từ các phụ thuộc;
/// lỗi đối chiếu hash lịch sử được coi là không khớp.
pub async fn reset_password(
    state: &AppState,
    ip: &str,
    token: &str,
    new_password: &str,
) -> AppResult<String> {
    state
        .rate_limiter
        .check_and_incr("reset_password", "ip", ip, 20, 900)
        .await?;

    let token_id = state
        .magic_link_signer
        .verify("password_reset", token)
        .map_err(|_| AppError::BadRequest("invalid token".into()))?;

    let password = new_password.to_string();
    let policy = state.password_policy.clone();
    let new_hash = with_argon_permit(state, move || policy.hash(&password)).await?;

    let mut tx = state.db.begin().await.map_err(|e| {
        tracing::error!(error = %e, "begin reset-password transaction failed");
        AppError::Internal
    })?;

    let consumed = TokenRepo::consume_tx(&mut tx, token_id, "password_reset")
        .await?
        .ok_or_else(|| AppError::BadRequest("invalid token".into()))?;

    let recent =
        CredentialRepo::list_recent_hashes_tx(&mut tx, consumed.user_id, PASSWORD_HISTORY_LIMIT)
            .await?;

    let policy_check = state.password_policy.clone();
    let new_password_owned = new_password.to_string();
    let reused = tokio::task::spawn_blocking(move || {
        for h in &recent {
            if policy_check.verify(&new_password_owned, h).unwrap_or(false) {
                return true;
            }
        }
        false
    })
    .await
    .map_err(|_| AppError::Internal)?;

    if reused {
        let _ = tx.rollback().await;
        return Err(AppError::BadRequest("password was used recently".into()));
    }

    CredentialRepo::update_password_tx(&mut tx, consumed.user_id, &new_hash).await?;

    let history_id = Uuid::now_v7();
    CredentialRepo::insert_history_tx(&mut tx, history_id, consumed.user_id, &new_hash).await?;
    CredentialRepo::trim_history_tx(&mut tx, consumed.user_id, PASSWORD_HISTORY_LIMIT).await?;

    SessionRepo::revoke_all_for_user_tx(&mut tx, consumed.user_id, "password_reset", None).await?;
    RefreshTokenRepo::revoke_all_for_user_tx(&mut tx, consumed.user_id, None).await?;

    insert_security_event_tx(
        &mut tx,
        "password_reset_completed",
        Some(consumed.user_id),
        None,
        Some(ip),
        None,
    )
    .await?;

    tx.commit().await.map_err(|e| {
        tracing::error!(error = %e, "commit reset-password failed");
        AppError::Internal
    })?;

    Ok("Password has been reset.".to_string())
}

/// Đổi mật khẩu của tài khoản active sau khi xác minh mật khẩu hiện tại và phiên.
///
/// Giữ phiên hiện tại, thu hồi các phiên khác cùng refresh token và giữ tối đa
/// năm hash lịch sử; trả thông báo thành công. Trả `Unauthorized` khi token,
/// phiên hoặc mật khẩu hiện tại không hợp lệ; trả `BadRequest` nếu mật khẩu mới
/// trùng mật khẩu hiện tại/lịch sử hoặc không dài 8–128 byte. Truyền lỗi
/// `TooManyRequests`, `Unavailable` và `Internal`; lỗi đối chiếu hash lịch sử
/// được coi là không khớp.
pub async fn change_password(
    state: &AppState,
    access_token: &str,
    ip: &str,
    current_password: &str,
    new_password: &str,
) -> AppResult<String> {
    let claims = state.jwt_verifier.verify_access_token(access_token)?;
    let user_id = Uuid::parse_str(&claims.sub).map_err(|_| AppError::Unauthorized)?;
    let session_id = Uuid::parse_str(&claims.sid).map_err(|_| AppError::Unauthorized)?;

    state
        .rate_limiter
        .check_and_incr("change_password", "user", &user_id.to_string(), 5, 900)
        .await?;
    state
        .rate_limiter
        .check_and_incr("change_password", "ip", ip, 20, 900)
        .await?;

    if current_password == new_password {
        return Err(AppError::BadRequest(
            "new password must differ from current password".into(),
        ));
    }

    let user = UserRepo::new(&state.db)
        .find_by_id(user_id)
        .await?
        .ok_or(AppError::Unauthorized)?;
    if user.status != "active" {
        return Err(AppError::Unauthorized);
    }

    let current = current_password.to_string();
    let new_pw = new_password.to_string();
    let policy = state.password_policy.clone();

    let session = SessionRepo::new(&state.db)
        .find_by_id(session_id)
        .await?
        .ok_or(AppError::Unauthorized)?;
    if session.user_id != user_id
        || session.revoked_at.is_some()
        || session.expires_at <= Utc::now()
    {
        return Err(AppError::Unauthorized);
    }

    let current_hash = CredentialRepo::find_password_hash(&state.db, user_id)
        .await?
        .ok_or(AppError::Unauthorized)?;

    let current_hash_for_verify = current_hash.clone();
    let policy_v = policy.clone();
    let current_ok = with_argon_permit(state, move || {
        policy_v.verify(&current, &current_hash_for_verify)
    })
    .await?;

    if !current_ok {
        return Err(AppError::Unauthorized);
    }

    let recent =
        CredentialRepo::list_recent_hashes(&state.db, user_id, PASSWORD_HISTORY_LIMIT).await?;

    let policy_h = policy.clone();
    let new_pw_check = new_pw.clone();
    let reused = with_argon_permit(state, move || {
        for h in &recent {
            if policy_h.verify(&new_pw_check, h).unwrap_or(false) {
                return Ok(true);
            }
        }
        Ok(false)
    })
    .await?;

    if reused {
        return Err(AppError::BadRequest("password was used recently".into()));
    }

    let new_hash = with_argon_permit(state, move || policy.hash(&new_pw)).await?;

    let mut tx = state.db.begin().await.map_err(|e| {
        tracing::error!(error = %e, "begin change-password transaction failed");
        AppError::Internal
    })?;

    let session = SessionRepo::find_by_id_for_update_tx(&mut tx, session_id)
        .await?
        .ok_or(AppError::Unauthorized)?;

    if session.user_id != user_id
        || session.revoked_at.is_some()
        || session.expires_at <= Utc::now()
    {
        let _ = tx.rollback().await;
        return Err(AppError::Unauthorized);
    }

    let locked_hash = CredentialRepo::find_password_hash_tx(&mut tx, user_id).await?;
    if locked_hash.as_deref() != Some(current_hash.as_str()) {
        let _ = tx.rollback().await;
        return Err(AppError::Unauthorized);
    }

    CredentialRepo::update_password_tx(&mut tx, user_id, &new_hash).await?;

    let history_id = Uuid::now_v7();
    CredentialRepo::insert_history_tx(&mut tx, history_id, user_id, &new_hash).await?;
    CredentialRepo::trim_history_tx(&mut tx, user_id, PASSWORD_HISTORY_LIMIT).await?;

    SessionRepo::revoke_all_for_user_tx(&mut tx, user_id, "password_change", Some(session_id))
        .await?;
    RefreshTokenRepo::revoke_all_for_user_tx(&mut tx, user_id, Some(session_id)).await?;

    insert_security_event_tx(
        &mut tx,
        "password_changed",
        Some(user_id),
        Some(session_id),
        Some(ip),
        None,
    )
    .await?;

    tx.commit().await.map_err(|e| {
        tracing::error!(error = %e, "commit change-password failed");
        AppError::Internal
    })?;

    Ok("Password has been changed.".to_string())
}

/// Đánh dấu tài khoản active để xóa vĩnh viễn sau 30 ngày và thu hồi mọi phiên
/// cùng refresh token; trả thông báo đã lên lịch. Yêu cầu access token và phiên
/// còn hợp lệ. Trả `Unauthorized` khi xác thực không đạt, `Conflict` khi không
/// thể chuyển trạng thái và `Internal` khi xác minh hoặc giao dịch lỗi.
pub async fn request_delete(state: &AppState, access_token: &str, ip: &str) -> AppResult<String> {
    let claims = state.jwt_verifier.verify_access_token(access_token)?;
    let user_id = Uuid::parse_str(&claims.sub).map_err(|_| AppError::Unauthorized)?;
    let session_id = Uuid::parse_str(&claims.sid).map_err(|_| AppError::Unauthorized)?;

    let session = SessionRepo::new(&state.db)
        .find_by_id(session_id)
        .await?
        .ok_or(AppError::Unauthorized)?;
    if session.user_id != user_id
        || session.revoked_at.is_some()
        || session.expires_at <= Utc::now()
    {
        return Err(AppError::Unauthorized);
    }

    let user = UserRepo::new(&state.db)
        .find_by_id(user_id)
        .await?
        .ok_or(AppError::Unauthorized)?;
    if user.status != "active" {
        return Err(AppError::Unauthorized);
    }

    let purge_after = Utc::now() + ChronoDuration::days(PURGE_AFTER_DAYS);

    let mut tx = state.db.begin().await.map_err(|_| AppError::Internal)?;

    UserRepo::soft_delete_tx(&mut tx, user_id, purge_after).await?;
    SessionRepo::revoke_all_for_user_tx(&mut tx, user_id, "account_deleted", None).await?;
    RefreshTokenRepo::revoke_all_for_user_tx(&mut tx, user_id, None).await?;

    insert_security_event_tx(
        &mut tx,
        "account_deleted",
        Some(user_id),
        None,
        Some(ip),
        None,
    )
    .await?;

    tx.commit().await.map_err(|_| AppError::Internal)?;
    Ok("Account scheduled for deletion.".to_string())
}

/// Thay token khôi phục bằng token hạn 48 giờ và xếp email cho tài khoản deleted.
///
/// Trả cùng thông báo trung tính khi email không tồn tại hoặc tài khoản chưa xóa.
/// Thời hạn purge được kiểm tra khi xác nhận khôi phục. Truyền lỗi
/// `TooManyRequests`/`Unavailable` từ giới hạn IP và `Internal` từ database.
pub async fn request_restore(state: &AppState, ip: &str, email_raw: &str) -> AppResult<String> {
    let email = normalize_email(email_raw);
    state
        .rate_limiter
        .check_and_incr("restore_request", "ip", ip, 10, 3600)
        .await?;

    let user_repo = UserRepo::new(&state.db);
    let Some((user, email_row)) = user_repo.find_by_email(&email).await? else {
        return Ok(NEUTRAL_RESTORE_MSG.to_string());
    };

    if user.status != "deleted" {
        return Ok(NEUTRAL_RESTORE_MSG.to_string());
    }

    let mut tx = state.db.begin().await.map_err(|_| AppError::Internal)?;
    TokenRepo::revoke_active_for_user_purpose_tx(&mut tx, user.id, "account_restore").await?;

    let token_id = Uuid::now_v7();
    let expires_at = Utc::now() + ChronoDuration::hours(RESTORE_TTL_HOURS);
    TokenRepo::insert_tx(
        &mut tx,
        token_id,
        user.id,
        "account_restore",
        &email_row.email,
        expires_at,
    )
    .await?;

    tx.commit().await.map_err(|_| AppError::Internal)?;
    Ok(NEUTRAL_RESTORE_MSG.to_string())
}

/// Tiêu thụ token account_restore và khôi phục trạng thái tài khoản trước khi xóa
/// trong cùng giao dịch, rồi trả thông báo thành công. Trả `BadRequest` khi token
/// không hợp lệ, tài khoản không còn deleted hoặc đã đến hạn purge; truyền lỗi
/// `Internal` từ truy vấn và giao dịch. Không khôi phục các phiên đã thu hồi.
pub async fn confirm_restore(state: &AppState, ip: &str, token: &str) -> AppResult<String> {
    let token_id = state
        .magic_link_signer
        .verify("account_restore", token)
        .map_err(|_| AppError::BadRequest("invalid token".into()))?;

    let mut tx = state.db.begin().await.map_err(|_| AppError::Internal)?;

    let consumed = TokenRepo::consume_tx(&mut tx, token_id, "account_restore")
        .await?
        .ok_or_else(|| AppError::BadRequest("invalid token".into()))?;

    UserRepo::restore_tx(&mut tx, consumed.user_id).await?;

    insert_security_event_tx(
        &mut tx,
        "account_restored",
        Some(consumed.user_id),
        None,
        Some(ip),
        None,
    )
    .await?;

    tx.commit().await.map_err(|_| AppError::Internal)?;
    Ok("Account restored.".to_string())
}
