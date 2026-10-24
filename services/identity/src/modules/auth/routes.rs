use std::net::SocketAddr;

use axum::extract::{ConnectInfo, State};
use axum::http::HeaderMap;
use axum::routing::post;
use axum::{Json, Router};
use validator::Validate;

use super::dto::{
    ChangePasswordRequest, ChangePasswordResponse, ForgotPasswordRequest, ForgotPasswordResponse,
    LoginRequest, LoginResponse, LogoutResponse, RefreshRequest, RefreshResponse, RegisterRequest,
    RegisterResponse, ResendVerificationRequest, ResendVerificationResponse, ResetPasswordRequest,
    ResetPasswordResponse, VerifyEmailRequest, VerifyEmailResponse,
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
}

fn client_ip(addr: Option<ConnectInfo<SocketAddr>>) -> String {
    addr.map(|a| a.0.ip().to_string())
        .unwrap_or_else(|| "unknown".to_string())
}

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

async fn register(
    State(state): State<AppState>,
    addr: Option<ConnectInfo<SocketAddr>>,
    Json(body): Json<RegisterRequest>,
) -> Result<Json<RegisterResponse>, AppError> {
    body.validate()
        .map_err(|_| AppError::BadRequest("invalid request".into()))?;
    let ip = client_ip(addr);
    let message = service::register(&state, &ip, body).await?;
    Ok(Json(RegisterResponse { message }))
}

async fn verify_email(
    State(state): State<AppState>,
    addr: Option<ConnectInfo<SocketAddr>>,
    Json(body): Json<VerifyEmailRequest>,
) -> Result<Json<VerifyEmailResponse>, AppError> {
    body.validate()
        .map_err(|_| AppError::BadRequest("invalid request".into()))?;
    let ip = client_ip(addr);
    let message = service::verify_email(&state, &ip, &body.token).await?;
    Ok(Json(VerifyEmailResponse { message }))
}

async fn resend_verification(
    State(state): State<AppState>,
    addr: Option<ConnectInfo<SocketAddr>>,
    Json(body): Json<ResendVerificationRequest>,
) -> Result<Json<ResendVerificationResponse>, AppError> {
    body.validate()
        .map_err(|_| AppError::BadRequest("invalid request".into()))?;
    let ip = client_ip(addr);
    let message = service::resend_verification(&state, &ip, &body.email).await?;
    Ok(Json(ResendVerificationResponse { message }))
}

async fn login(
    State(state): State<AppState>,
    addr: Option<ConnectInfo<SocketAddr>>,
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

async fn logout(
    State(state): State<AppState>,
    addr: Option<ConnectInfo<SocketAddr>>,
    headers: HeaderMap,
) -> Result<Json<LogoutResponse>, AppError> {
    let token = extract_bearer(&headers)?;
    let ip = client_ip(addr);
    let message = service::logout(&state, token, &ip).await?;
    Ok(Json(LogoutResponse { message }))
}

async fn refresh(
    State(state): State<AppState>,
    addr: Option<ConnectInfo<SocketAddr>>,
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

async fn forgot_password(
    State(state): State<AppState>,
    addr: Option<ConnectInfo<SocketAddr>>,
    Json(body): Json<ForgotPasswordRequest>,
) -> Result<Json<ForgotPasswordResponse>, AppError> {
    body.validate()
        .map_err(|_| AppError::BadRequest("invalid request".into()))?;
    let ip = client_ip(addr);
    let message = service::forgot_password(&state, &ip, &body.email).await?;
    Ok(Json(ForgotPasswordResponse { message }))
}

async fn reset_password(
    State(state): State<AppState>,
    addr: Option<ConnectInfo<SocketAddr>>,
    Json(body): Json<ResetPasswordRequest>,
) -> Result<Json<ResetPasswordResponse>, AppError> {
    body.validate()
        .map_err(|_| AppError::BadRequest("invalid request".into()))?;
    let ip = client_ip(addr);
    let message = service::reset_password(&state, &ip, &body.token, &body.new_password).await?;
    Ok(Json(ResetPasswordResponse { message }))
}

async fn change_password(
    State(state): State<AppState>,
    addr: Option<ConnectInfo<SocketAddr>>,
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
