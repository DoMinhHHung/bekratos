use uuid::Uuid;

use crate::error::{AppError, AppResult};
use crate::modules::session::repo::{RefreshTokenRepo, SessionRepo};
use crate::modules::user::dto::{
    ProfileResponse, SessionItem, SessionListResponse, UpdateProfileRequest,
};
use crate::modules::user::repo::UserRepo;
use crate::state::AppState;

/// Xác minh access token, tài khoản active và phiên đúng chủ sở hữu, còn hạn.
///
/// Trả cặp ID người dùng/phiên; xác thực không đạt trả `Unauthorized`, còn lỗi
/// bộ xác minh hoặc truy vấn được truyền lên dưới dạng `Internal`.
async fn require_active_session(state: &AppState, access_token: &str) -> AppResult<(Uuid, Uuid)> {
    let claims = state.jwt_verifier.verify_access_token(access_token)?;
    let user_id = Uuid::parse_str(&claims.sub).map_err(|_| AppError::Unauthorized)?;
    let session_id = Uuid::parse_str(&claims.sid).map_err(|_| AppError::Unauthorized)?;

    let session = SessionRepo::new(&state.db)
        .find_by_id(session_id)
        .await?
        .ok_or(AppError::Unauthorized)?;
    if session.user_id != user_id
        || session.revoked_at.is_some()
        || session.expires_at <= chrono::Utc::now()
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

    Ok((user_id, session_id))
}

/// Trả hồ sơ, email chính và username của người dùng có tài khoản active và phiên hợp lệ.
///
/// Trả `Unauthorized` khi xác thực không đạt hoặc không còn người dùng;
/// truyền lỗi `Internal` từ bộ xác minh và database.
pub async fn get_profile(state: &AppState, access_token: &str) -> AppResult<ProfileResponse> {
    let (user_id, _) = require_active_session(state, access_token).await?;

    let repo = UserRepo::new(&state.db);
    let bundle = repo
        .load_profile_bundle(user_id)
        .await?
        .ok_or(AppError::Unauthorized)?;

    Ok(ProfileResponse {
        id: user_id,
        status: bundle.user.status,
        email: bundle.email,
        email_verified: bundle.email_verified,
        username: bundle.username,
        full_name: bundle.full_name,
        display_name: bundle.display_name,
        avatar_url: bundle.avatar_url,
        locale: bundle.user.locale,
        timezone: bundle.user.timezone,
    })
}

/// Cập nhật các trường hồ sơ được cung cấp và trả hồ sơ đọc lại sau commit.
///
/// Trường `None` giữ giá trị hiện có. Yêu cầu tài khoản active và phiên hợp lệ;
/// truyền `Unauthorized` khi xác thực không đạt và `Internal` khi xử lý lỗi,
/// kể cả lúc đọc lại sau khi thay đổi đã được lưu.
pub async fn update_profile(
    state: &AppState,
    access_token: &str,
    body: UpdateProfileRequest,
) -> AppResult<ProfileResponse> {
    let (user_id, _) = require_active_session(state, access_token).await?;

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

/// Liệt kê phiên chưa thu hồi, chưa hết hạn theo lần hoạt động gần nhất trước,
/// đồng thời đánh dấu phiên hiện tại. Yêu cầu tài khoản active và phiên hợp lệ;
/// truyền lỗi `Unauthorized` hoặc `Internal` từ xác minh và truy vấn.
pub async fn list_sessions(state: &AppState, access_token: &str) -> AppResult<SessionListResponse> {
    let (user_id, current_sid) = require_active_session(state, access_token).await?;

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

/// Thu hồi một phiên khác của người dùng cùng các refresh token của phiên đó
/// và trả thông báo thành công. Trả `BadRequest` nếu chọn phiên hiện tại,
/// `NotFound` nếu phiên đích không tồn tại hoặc thuộc người khác; truyền lỗi
/// `Unauthorized`/`Internal` từ xác thực, truy vấn và giao dịch.
pub async fn revoke_session(
    state: &AppState,
    access_token: &str,
    target_session_id: Uuid,
) -> AppResult<String> {
    let (user_id, current_sid) = require_active_session(state, access_token).await?;

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

/// Thu hồi mọi phiên khác cùng refresh token, giữ phiên hiện tại và trả thông báo.
///
/// Yêu cầu tài khoản active và phiên hợp lệ; truyền lỗi `Unauthorized` hoặc
/// `Internal` từ xác thực, truy vấn và giao dịch.
pub async fn revoke_other_sessions(state: &AppState, access_token: &str) -> AppResult<String> {
    let (user_id, current_sid) = require_active_session(state, access_token).await?;

    let mut tx = state.db.begin().await.map_err(|_| AppError::Internal)?;

    SessionRepo::revoke_all_for_user_tx(&mut tx, user_id, "user_revoke_others", Some(current_sid))
        .await?;
    RefreshTokenRepo::revoke_all_for_user_tx(&mut tx, user_id, Some(current_sid)).await?;

    tx.commit().await.map_err(|_| AppError::Internal)?;
    Ok("Other sessions revoked.".to_string())
}
