use std::net::SocketAddr;

use axum::extract::{ConnectInfo, Extension, Path, State};
use axum::http::HeaderMap;
use axum::routing::{delete, get};
use axum::{Json, Router};
use uuid::Uuid;
use validator::Validate;

use super::dto::{MessageResponse, ProfileResponse, SessionListResponse, UpdateProfileRequest};
use super::service;
use crate::error::AppError;
use crate::state::AppState;

/// Lấy token không rỗng sau tiền tố `Bearer ` hoặc `bearer ` trong Authorization.
///
/// Trả `Unauthorized` khi thiếu header, không đọc được hoặc sai định dạng.
fn extract_bearer(headers: &HeaderMap) -> Result<&str, AppError> {
    let value = headers
        .get(axum::http::header::AUTHORIZATION)
        .and_then(|v| v.to_str().ok())
        .ok_or(AppError::Unauthorized)?;
    value
        .strip_prefix("Bearer ")
        .or_else(|| value.strip_prefix("bearer "))
        .filter(|t| !t.is_empty())
        .ok_or(AppError::Unauthorized)
}

pub fn router() -> Router<AppState> {
    Router::new()
        .route("/v1/me", get(get_profile).patch(update_profile))
        .route("/v1/me/sessions", get(list_sessions).delete(revoke_others))
        .route("/v1/me/sessions/{id}", delete(revoke_one))
        .route("/v1/me/delete", delete(delete_account))
}

/// Lấy IP từ kết nối trực tiếp, hoặc `unknown` khi thiếu thông tin kết nối.
fn client_ip(addr: Option<Extension<ConnectInfo<SocketAddr>>>) -> String {
    addr.map(|Extension(ConnectInfo(peer))| peer.ip().to_string())
        .unwrap_or_else(|| "unknown".to_string())
}

/// Trả hồ sơ người dùng được xác thực bằng Bearer token và phiên còn hợp lệ.
///
/// Truyền lỗi `Unauthorized` từ xác thực và `Internal` từ xử lý/truy vấn.
#[utoipa::path(
    get,
    path = "/v1/me",
    responses(
        (status = 200, description = "Current user profile", body = ProfileResponse),
        (status = 401, description = "Invalid access token")
    ),
    security(("bearer_auth" = [])),
    tag = "Users"
)]
pub(crate) async fn get_profile(
    State(state): State<AppState>,
    headers: HeaderMap,
) -> Result<Json<ProfileResponse>, AppError> {
    let token = extract_bearer(&headers)?;
    let profile = service::get_profile(&state, token).await?;
    Ok(Json(profile))
}

/// Cập nhật các trường hồ sơ được cung cấp, giữ trường bỏ trống và trả hồ sơ mới.
///
/// Trả `BadRequest` khi dữ liệu không hợp lệ; truyền lỗi `Unauthorized` và `Internal`
/// từ xác thực, cập nhật hoặc đọc lại hồ sơ sau khi đã lưu.
#[utoipa::path(
    patch,
    path = "/v1/me",
    request_body = UpdateProfileRequest,
    responses(
        (status = 200, description = "Updated user profile", body = ProfileResponse),
        (status = 400, description = "Invalid request"),
        (status = 401, description = "Invalid access token")
    ),
    security(("bearer_auth" = [])),
    tag = "Users"
)]
pub(crate) async fn update_profile(
    State(state): State<AppState>,
    headers: HeaderMap,
    Json(body): Json<UpdateProfileRequest>,
) -> Result<Json<ProfileResponse>, AppError> {
    body.validate()
        .map_err(|_| AppError::BadRequest("invalid request".into()))?;
    let token = extract_bearer(&headers)?;
    let profile = service::update_profile(&state, token, body).await?;
    Ok(Json(profile))
}

/// Trả các phiên còn hiệu lực và đánh dấu phiên hiện tại của người dùng.
///
/// Truyền lỗi `Unauthorized` khi xác thực không đạt và `Internal` khi xử lý lỗi.
#[utoipa::path(
    get,
    path = "/v1/me/sessions",
    responses(
        (status = 200, description = "Active sessions", body = SessionListResponse),
        (status = 401, description = "Invalid access token")
    ),
    security(("bearer_auth" = [])),
    tag = "Sessions"
)]
pub(crate) async fn list_sessions(
    State(state): State<AppState>,
    headers: HeaderMap,
) -> Result<Json<SessionListResponse>, AppError> {
    let token = extract_bearer(&headers)?;
    let list = service::list_sessions(&state, token).await?;
    Ok(Json(list))
}

/// Thu hồi phiên khác theo ID cùng refresh token của phiên và trả thông báo.
///
/// Trả `BadRequest` nếu chọn phiên hiện tại, `NotFound` nếu phiên không có/khác
/// chủ sở hữu; truyền lỗi `Unauthorized` và `Internal` từ dịch vụ.
#[utoipa::path(
    delete,
    path = "/v1/me/sessions/{id}",
    params(("id" = Uuid, Path, description = "Session identifier")),
    responses(
        (status = 200, description = "Session revoked", body = MessageResponse),
        (status = 401, description = "Invalid access token"),
        (status = 404, description = "Session not found")
    ),
    security(("bearer_auth" = [])),
    tag = "Sessions"
)]
pub(crate) async fn revoke_one(
    State(state): State<AppState>,
    headers: HeaderMap,
    Path(id): Path<Uuid>,
) -> Result<Json<MessageResponse>, AppError> {
    let token = extract_bearer(&headers)?;
    let message = service::revoke_session(&state, token, id).await?;
    Ok(Json(MessageResponse { message }))
}

/// Thu hồi các phiên khác cùng refresh token, giữ phiên hiện tại và trả thông báo.
///
/// Truyền lỗi `Unauthorized` khi xác thực không đạt và `Internal` khi xử lý lỗi.
#[utoipa::path(
    delete,
    path = "/v1/me/sessions",
    responses(
        (status = 200, description = "Other sessions revoked", body = MessageResponse),
        (status = 401, description = "Invalid access token")
    ),
    security(("bearer_auth" = [])),
    tag = "Sessions"
)]
pub(crate) async fn revoke_others(
    State(state): State<AppState>,
    headers: HeaderMap,
) -> Result<Json<MessageResponse>, AppError> {
    let token = extract_bearer(&headers)?;
    let message = service::revoke_other_sessions(&state, token).await?;
    Ok(Json(MessageResponse { message }))
}

/// Lên lịch xóa tài khoản sau 30 ngày và thu hồi mọi phiên cùng refresh token.
///
/// Trả thông báo đã lên lịch hoặc truyền lỗi `Unauthorized`, `Conflict`, `Internal`
/// từ xác thực và giao dịch.
#[utoipa::path(
    delete,
    path = "/v1/me/delete",
    responses(
        (status = 200, description = "Account deletion scheduled", body = MessageResponse),
        (status = 401, description = "Invalid access token"),
        (status = 429, description = "Rate limit exceeded")
    ),
    security(("bearer_auth" = [])),
    tag = "Users"
)]
pub(crate) async fn delete_account(
    State(state): State<AppState>,
    addr: Option<Extension<ConnectInfo<SocketAddr>>>,
    headers: HeaderMap,
) -> Result<Json<MessageResponse>, AppError> {
    let token = extract_bearer(&headers)?;
    let ip = client_ip(addr);
    let message = crate::modules::auth::service::request_delete(&state, token, &ip).await?;
    Ok(Json(MessageResponse { message }))
}

#[cfg(test)]
mod tests {
    use super::client_ip;
    use axum::extract::{ConnectInfo, Extension};
    use std::net::{IpAddr, Ipv6Addr, SocketAddr};

    #[test]
    fn client_ip_uses_only_the_connection_peer() {
        let peer = SocketAddr::new(IpAddr::V6(Ipv6Addr::LOCALHOST), 8080);
        assert_eq!(client_ip(Some(Extension(ConnectInfo(peer)))), "::1");
        assert_eq!(client_ip(None), "unknown");
    }
}
