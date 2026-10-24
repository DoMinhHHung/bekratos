use uuid::Uuid;

use crate::error::{AppError, AppResult};
use crate::modules::session::repo::{RefreshTokenRepo, SessionRepo};
use crate::modules::user::dto::{
    ProfileResponse, SessionItem, SessionListResponse, UpdateProfileRequest,
};
use crate::modules::user::repo::UserRepo;
use crate::state::AppState;

pub async fn get_profile(state: &AppState, access_token: &str) -> AppResult<ProfileResponse> {
    let claims = state.jwt_verifier.verify_access_token(access_token)?;
    let user_id = Uuid::parse_str(&claims.sub).map_err(|_| AppError::Unauthorized)?;

    let repo = UserRepo::new(&state.db);
    let user = repo
        .find_by_id(user_id)
        .await?
        .ok_or(AppError::Unauthorized)?;
    if user.status != "active" {
        return Err(AppError::Unauthorized);
    }

    let profile = repo.find_profile(user_id).await?;
    let (status, full_name, display_name, avatar_url) =
        profile.unwrap_or((user.status.clone(), None, None, None));
    let email = repo.find_primary_email(user_id).await?;
    let username = repo.find_username(user_id).await?;

    Ok(ProfileResponse {
        id: user_id,
        status,
        email,
        username,
        full_name,
        display_name,
        avatar_url,
        locale: user.locale,
        timezone: user.timezone,
    })
}

pub async fn update_profile(
    state: &AppState,
    access_token: &str,
    body: UpdateProfileRequest,
) -> AppResult<ProfileResponse> {
    let claims = state.jwt_verifier.verify_access_token(access_token)?;
    let user_id = Uuid::parse_str(&claims.sub).map_err(|_| AppError::Unauthorized)?;

    let mut tx = state.db.begin().await.map_err(|_| AppError::Internal)?;

    UserRepo::update_profile_tx(
        &mut tx,
        user_id,
        body.full_name.as_deref(),
        body.display_name.as_deref(),
        body.avatar_url.as_deref(),
    )
    .await?;

    UserRepo::update_user_meta_tx(
        &mut tx,
        user_id,
        body.locale.as_deref(),
        body.timezone.as_deref(),
    )
    .await?;

    tx.commit().await.map_err(|_| AppError::Internal)?;

    get_profile(state, access_token).await
}

pub async fn list_sessions(state: &AppState, access_token: &str) -> AppResult<SessionListResponse> {
    let claims = state.jwt_verifier.verify_access_token(access_token)?;
    let user_id = Uuid::parse_str(&claims.sub).map_err(|_| AppError::Unauthorized)?;
    let current_sid = Uuid::parse_str(&claims.sid).map_err(|_| AppError::Unauthorized)?;

    let repo = SessionRepo::new(&state.db);
    let rows = repo.list_active_for_user(user_id).await?;

    let sessions = rows
        .into_iter()
        .map(|s| SessionItem {
            id: s.id,
            ip: s.ip,
            user_agent: s.user_agent,
            device_name: s.device_name,
            last_seen_at: s.last_seen_at,
            expires_at: s.expires_at,
            created_at: s.created_at,
            is_current: s.id == current_sid,
        })
        .collect();

    Ok(SessionListResponse { sessions })
}

pub async fn revoke_session(
    state: &AppState,
    access_token: &str,
    target_session_id: Uuid,
) -> AppResult<String> {
    let claims = state.jwt_verifier.verify_access_token(access_token)?;
    let user_id = Uuid::parse_str(&claims.sub).map_err(|_| AppError::Unauthorized)?;
    let current_sid = Uuid::parse_str(&claims.sid).map_err(|_| AppError::Unauthorized)?;

    if target_session_id == current_sid {
        return Err(AppError::BadRequest(
            "cannot revoke current session; use logout".into(),
        ));
    }

    let mut tx = state.db.begin().await.map_err(|_| AppError::Internal)?;

    let session = SessionRepo::find_by_id_for_update_tx(&mut tx, target_session_id)
        .await?
        .ok_or(AppError::NotFound)?;

    if session.user_id != user_id {
        return Err(AppError::NotFound);
    }

    SessionRepo::revoke_tx(&mut tx, target_session_id, "user_revoke").await?;
    RefreshTokenRepo::revoke_all_for_session_tx(&mut tx, target_session_id).await?;

    tx.commit().await.map_err(|_| AppError::Internal)?;
    Ok("Session revoked.".to_string())
}

pub async fn revoke_other_sessions(state: &AppState, access_token: &str) -> AppResult<String> {
    let claims = state.jwt_verifier.verify_access_token(access_token)?;
    let user_id = Uuid::parse_str(&claims.sub).map_err(|_| AppError::Unauthorized)?;
    let current_sid = Uuid::parse_str(&claims.sid).map_err(|_| AppError::Unauthorized)?;

    let mut tx = state.db.begin().await.map_err(|_| AppError::Internal)?;

    SessionRepo::revoke_all_for_user_tx(&mut tx, user_id, "user_revoke_others", Some(current_sid))
        .await?;
    RefreshTokenRepo::revoke_all_for_user_tx(&mut tx, user_id, Some(current_sid)).await?;

    tx.commit().await.map_err(|_| AppError::Internal)?;
    Ok("Other sessions revoked.".to_string())
}
