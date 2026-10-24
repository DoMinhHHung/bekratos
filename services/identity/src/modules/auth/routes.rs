use std::net::SocketAddr;

use axum::extract::{ConnectInfo, Extension, State};
use axum::http::HeaderMap;
use axum::routing::post;
use axum::{Json, Router};
use validator::Validate;

use super::dto::{
    ChangePasswordRequest, ChangePasswordResponse, ForgotPasswordRequest, ForgotPasswordResponse,
    LoginRequest, LoginResponse, LogoutResponse, RefreshRequest, RefreshResponse, RegisterRequest,
    RegisterResponse, ResendVerificationRequest, ResendVerificationResponse, ResetPasswordRequest,
    ResetPasswordResponse, RestoreConfirmRequest, RestoreRequestBody, RestoreResponse,
    VerifyEmailRequest, VerifyEmailResponse,
};
use super::service;
use crate::error::AppError;
use crate::state::AppState;

pub fn router() -> Router<AppState> {
    Router::new()
        .route("/v1/auth/register", post(register))
        .route("/v1/auth/verify-email", post(verify_email))
        .route("/v1/auth/resend-verification", post(resend_verification))
        .route("/v1/auth/login", post(login))
        .route("/v1/auth/logout", post(logout))
        .route("/v1/auth/refresh", post(refresh))
        .route("/v1/auth/forgot-password", post(forgot_password))
        .route("/v1/auth/reset-password", post(reset_password))
        .route("/v1/auth/change-password", post(change_password))
        .route("/v1/auth/request-restore", post(request_restore))
        .route("/v1/auth/restore", post(confirm_restore))
}

/// Lấy IP từ kết nối trực tiếp, hoặc `unknown` khi thiếu thông tin kết nối.
fn client_ip(addr: Option<Extension<ConnectInfo<SocketAddr>>>) -> String {
    addr.map(|Extension(ConnectInfo(peer))| peer.ip().to_string())
        .unwrap_or_else(|| "unknown".to_string())
}

/// Lấy token không rỗng sau tiền tố `Bearer ` hoặc `bearer ` trong Authorization.
///
/// Trả `Unauthorized` khi thiếu header, không đọc được hoặc sai định dạng.
fn extract_bearer(headers: &HeaderMap) -> Result<&str, AppError> {
    let value = headers
        .get(axum::http::header::AUTHORIZATION)
        .and_then(|v| v.to_str().ok())
        .ok_or(AppError::Unauthorized)?;
    let token = value
        .strip_prefix("Bearer ")
        .or_else(|| value.strip_prefix("bearer "))
        .ok_or(AppError::Unauthorized)?;
    if token.is_empty() {
        return Err(AppError::Unauthorized);
    }
    Ok(token)
}

/// Tiếp nhận đăng ký và trả thông báo trung tính, xếp email xác minh khi phù hợp.
///
/// Dữ liệu không hợp lệ trả `BadRequest`; truyền lỗi giới hạn yêu cầu
/// `TooManyRequests`/`Unavailable` và lỗi xử lý `Internal`.
#[utoipa::path(
    post,
    path = "/v1/auth/register",
    request_body = RegisterRequest,
    responses(
        (status = 200, description = "Registration request accepted", body = RegisterResponse),
        (status = 400, description = "Invalid request"),
        (status = 429, description = "Rate limit exceeded")
    ),
    tag = "Authentication"
)]
pub(crate) async fn register(
    State(state): State<AppState>,
    addr: Option<Extension<ConnectInfo<SocketAddr>>>,
    Json(body): Json<RegisterRequest>,
) -> Result<Json<RegisterResponse>, AppError> {
    body.validate()
        .map_err(|_| AppError::BadRequest("invalid request".into()))?;
    let ip = client_ip(addr);
    let message = service::register(&state, &ip, body).await?;
    Ok(Json(RegisterResponse { message }))
}

/// Xác minh email bằng token dùng một lần và trả thông báo thành công.
///
/// Dữ liệu/token không hợp lệ trả `BadRequest`; truyền lỗi `TooManyRequests`,
/// `Unavailable` và `Internal` từ dịch vụ.
#[utoipa::path(
    post,
    path = "/v1/auth/verify-email",
    request_body = VerifyEmailRequest,
    responses(
        (status = 200, description = "Email verified", body = VerifyEmailResponse),
        (status = 400, description = "Invalid or expired token"),
        (status = 429, description = "Rate limit exceeded")
    ),
    tag = "Authentication"
)]
pub(crate) async fn verify_email(
    State(state): State<AppState>,
    addr: Option<Extension<ConnectInfo<SocketAddr>>>,
    Json(body): Json<VerifyEmailRequest>,
) -> Result<Json<VerifyEmailResponse>, AppError> {
    body.validate()
        .map_err(|_| AppError::BadRequest("invalid request".into()))?;
    let ip = client_ip(addr);
    let message = service::verify_email(&state, &ip, &body.token).await?;
    Ok(Json(VerifyEmailResponse { message }))
}

/// Yêu cầu email xác minh mới cho tài khoản pending và trả thông báo trung tính.
///
/// Dữ liệu không hợp lệ trả `BadRequest`; truyền lỗi `TooManyRequests`,
/// `Unavailable` và `Internal` từ dịch vụ.
#[utoipa::path(
    post,
    path = "/v1/auth/resend-verification",
    request_body = ResendVerificationRequest,
    responses(
        (status = 200, description = "Request accepted", body = ResendVerificationResponse),
        (status = 400, description = "Invalid request"),
        (status = 429, description = "Rate limit exceeded")
    ),
    tag = "Authentication"
)]
pub(crate) async fn resend_verification(
    State(state): State<AppState>,
    addr: Option<Extension<ConnectInfo<SocketAddr>>>,
    Json(body): Json<ResendVerificationRequest>,
) -> Result<Json<ResendVerificationResponse>, AppError> {
    body.validate()
        .map_err(|_| AppError::BadRequest("invalid request".into()))?;
    let ip = client_ip(addr);
    let message = service::resend_verification(&state, &ip, &body.email).await?;
    Ok(Json(ResendVerificationResponse { message }))
}

/// Đăng nhập và trả cặp token cùng thời hạn access token tính bằng giây.
///
/// Dữ liệu không hợp lệ trả `BadRequest`, xác thực không đạt trả `Unauthorized`;
/// truyền lỗi `TooManyRequests`, `Unavailable` và `Internal` từ dịch vụ.
#[utoipa::path(
    post,
    path = "/v1/auth/login",
    request_body = LoginRequest,
    responses(
        (status = 200, description = "Login successful", body = LoginResponse),
        (status = 400, description = "Invalid request"),
        (status = 401, description = "Invalid credentials"),
        (status = 429, description = "Rate limit exceeded")
    ),
    tag = "Authentication"
)]
pub(crate) async fn login(
    State(state): State<AppState>,
    addr: Option<Extension<ConnectInfo<SocketAddr>>>,
    headers: HeaderMap,
    Json(body): Json<LoginRequest>,
) -> Result<Json<LoginResponse>, AppError> {
    body.validate()
        .map_err(|_| AppError::BadRequest("invalid request".into()))?;
    let ip = client_ip(addr);
    let ua = headers
        .get(axum::http::header::USER_AGENT)
        .and_then(|v| v.to_str().ok());
    let resp = service::login(&state, &ip, ua, body).await?;
    Ok(Json(resp))
}

/// Thu hồi phiên từ Bearer token cùng refresh token và trả thông báo đăng xuất.
///
/// Truyền lỗi `Unauthorized` khi xác thực không đạt và `Internal` khi xử lý lỗi.
#[utoipa::path(
    post,
    path = "/v1/auth/logout",
    responses(
        (status = 200, description = "Logged out", body = LogoutResponse),
        (status = 401, description = "Invalid access token")
    ),
    security(("bearer_auth" = [])),
    tag = "Authentication"
)]
pub(crate) async fn logout(
    State(state): State<AppState>,
    addr: Option<Extension<ConnectInfo<SocketAddr>>>,
    headers: HeaderMap,
) -> Result<Json<LogoutResponse>, AppError> {
    let token = extract_bearer(&headers)?;
    let ip = client_ip(addr);
    let message = service::logout(&state, token, &ip).await?;
    Ok(Json(LogoutResponse { message }))
}

/// Đổi refresh token dùng một lần lấy cặp token mới; thời hạn trả về tính bằng giây.
///
/// Tái sử dụng token đã dùng/thu hồi làm thu hồi phiên. Dữ liệu không hợp lệ trả
/// `BadRequest`; truyền lỗi `Unauthorized`, `TooManyRequests`, `Unavailable`
/// và `Internal` từ dịch vụ.
#[utoipa::path(
    post,
    path = "/v1/auth/refresh",
    request_body = RefreshRequest,
    responses(
        (status = 200, description = "Tokens refreshed", body = RefreshResponse),
        (status = 400, description = "Invalid request"),
        (status = 401, description = "Invalid or reused refresh token"),
        (status = 429, description = "Rate limit exceeded")
    ),
    tag = "Authentication"
)]
pub(crate) async fn refresh(
    State(state): State<AppState>,
    addr: Option<Extension<ConnectInfo<SocketAddr>>>,
    headers: HeaderMap,
    Json(body): Json<RefreshRequest>,
) -> Result<Json<RefreshResponse>, AppError> {
    body.validate()
        .map_err(|_| AppError::BadRequest("invalid request".into()))?;
    let ip = client_ip(addr);
    let ua = headers
        .get(axum::http::header::USER_AGENT)
        .and_then(|v| v.to_str().ok());
    let resp = service::refresh(&state, &ip, ua, &body.refresh_token).await?;
    Ok(Json(RefreshResponse {
        access_token: resp.access_token,
        refresh_token: resp.refresh_token,
        token_type: resp.token_type,
        expires_in: resp.expires_in,
    }))
}

/// Yêu cầu email đặt lại mật khẩu và trả thông báo trung tính về tài khoản.
///
/// Dữ liệu không hợp lệ trả `BadRequest`; truyền lỗi `TooManyRequests`,
/// `Unavailable` và `Internal` từ dịch vụ.
#[utoipa::path(
    post,
    path = "/v1/auth/forgot-password",
    request_body = ForgotPasswordRequest,
    responses(
        (status = 200, description = "Request accepted", body = ForgotPasswordResponse),
        (status = 400, description = "Invalid request"),
        (status = 429, description = "Rate limit exceeded")
    ),
    tag = "Authentication"
)]
pub(crate) async fn forgot_password(
    State(state): State<AppState>,
    addr: Option<Extension<ConnectInfo<SocketAddr>>>,
    Json(body): Json<ForgotPasswordRequest>,
) -> Result<Json<ForgotPasswordResponse>, AppError> {
    body.validate()
        .map_err(|_| AppError::BadRequest("invalid request".into()))?;
    let ip = client_ip(addr);
    let message = service::forgot_password(&state, &ip, &body.email).await?;
    Ok(Json(ForgotPasswordResponse { message }))
}

/// Đặt mật khẩu qua token dùng một lần và thu hồi mọi phiên đăng nhập.
///
/// Dữ liệu/token/mật khẩu không hợp lệ trả `BadRequest`; truyền lỗi
/// `TooManyRequests`, `Unavailable` và `Internal` từ dịch vụ.
#[utoipa::path(
    post,
    path = "/v1/auth/reset-password",
    request_body = ResetPasswordRequest,
    responses(
        (status = 200, description = "Password reset", body = ResetPasswordResponse),
        (status = 400, description = "Invalid or expired token"),
        (status = 429, description = "Rate limit exceeded")
    ),
    tag = "Authentication"
)]
pub(crate) async fn reset_password(
    State(state): State<AppState>,
    addr: Option<Extension<ConnectInfo<SocketAddr>>>,
    Json(body): Json<ResetPasswordRequest>,
) -> Result<Json<ResetPasswordResponse>, AppError> {
    body.validate()
        .map_err(|_| AppError::BadRequest("invalid request".into()))?;
    let ip = client_ip(addr);
    let message = service::reset_password(&state, &ip, &body.token, &body.new_password).await?;
    Ok(Json(ResetPasswordResponse { message }))
}

/// Đổi mật khẩu sau khi xác thực Bearer token và mật khẩu hiện tại, giữ phiên hiện tại.
///
/// Truyền lỗi `BadRequest` khi dữ liệu/mật khẩu mới không hợp lệ, `Unauthorized`
/// khi xác thực không đạt và `TooManyRequests`, `Unavailable`, `Internal` khi xử lý lỗi.
#[utoipa::path(
    post,
    path = "/v1/auth/change-password",
    request_body = ChangePasswordRequest,
    responses(
        (status = 200, description = "Password changed", body = ChangePasswordResponse),
        (status = 400, description = "Invalid request"),
        (status = 401, description = "Invalid access token or current password"),
        (status = 429, description = "Rate limit exceeded")
    ),
    security(("bearer_auth" = [])),
    tag = "Authentication"
)]
pub(crate) async fn change_password(
    State(state): State<AppState>,
    addr: Option<Extension<ConnectInfo<SocketAddr>>>,
    headers: HeaderMap,
    Json(body): Json<ChangePasswordRequest>,
) -> Result<Json<ChangePasswordResponse>, AppError> {
    body.validate()
        .map_err(|_| AppError::BadRequest("invalid request".into()))?;
    let token = extract_bearer(&headers)?;
    let ip = client_ip(addr);
    let message = service::change_password(
        &state,
        token,
        &ip,
        &body.current_password,
        &body.new_password,
    )
    .await?;
    Ok(Json(ChangePasswordResponse { message }))
}

/// Yêu cầu email khôi phục tài khoản đã xóa và trả thông báo trung tính.
///
/// Dữ liệu không hợp lệ trả `BadRequest`; truyền lỗi `TooManyRequests`,
/// `Unavailable` và `Internal` từ dịch vụ.
#[utoipa::path(
    post,
    path = "/v1/auth/request-restore",
    request_body = RestoreRequestBody,
    responses(
        (status = 200, description = "Request accepted", body = RestoreResponse),
        (status = 400, description = "Invalid request"),
        (status = 429, description = "Rate limit exceeded")
    ),
    tag = "Authentication"
)]
pub(crate) async fn request_restore(
    State(state): State<AppState>,
    addr: Option<Extension<ConnectInfo<SocketAddr>>>,
    Json(body): Json<RestoreRequestBody>,
) -> Result<Json<RestoreResponse>, AppError> {
    body.validate()
        .map_err(|_| AppError::BadRequest("invalid request".into()))?;
    let ip = client_ip(addr);
    let message = service::request_restore(&state, &ip, &body.email).await?;
    Ok(Json(RestoreResponse { message }))
}

/// Khôi phục tài khoản bằng token dùng một lần và trả thông báo thành công.
///
/// Truyền `BadRequest` khi dữ liệu/token hoặc điều kiện khôi phục không hợp lệ,
/// `Internal` khi truy vấn hoặc giao dịch lỗi.
#[utoipa::path(
    post,
    path = "/v1/auth/restore",
    request_body = RestoreConfirmRequest,
    responses(
        (status = 200, description = "Account restored", body = RestoreResponse),
        (status = 400, description = "Invalid or expired token"),
        (status = 429, description = "Rate limit exceeded")
    ),
    tag = "Authentication"
)]
pub(crate) async fn confirm_restore(
    State(state): State<AppState>,
    addr: Option<Extension<ConnectInfo<SocketAddr>>>,
    Json(body): Json<RestoreConfirmRequest>,
) -> Result<Json<RestoreResponse>, AppError> {
    body.validate()
        .map_err(|_| AppError::BadRequest("invalid request".into()))?;
    let ip = client_ip(addr);
    let message = service::confirm_restore(&state, &ip, &body.token).await?;
    Ok(Json(RestoreResponse { message }))
}

#[cfg(test)]
mod tests {
    use super::client_ip;
    use axum::extract::{ConnectInfo, Extension};
    use std::net::{IpAddr, Ipv4Addr, SocketAddr};

    #[test]
    fn client_ip_uses_only_the_connection_peer() {
        let peer = SocketAddr::new(IpAddr::V4(Ipv4Addr::new(192, 0, 2, 10)), 8080);
        assert_eq!(client_ip(Some(Extension(ConnectInfo(peer)))), "192.0.2.10");
        assert_eq!(client_ip(None), "unknown");
    }
}
