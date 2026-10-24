use std::net::SocketAddr;

use axum::extract::ConnectInfo;
use axum::extract::{Path, State};
use axum::http::HeaderMap;
use axum::routing::{delete, get};
use axum::{Json, Router};
use uuid::Uuid;
use validator::Validate;

use super::dto::{MessageResponse, ProfileResponse, SessionListResponse, UpdateProfileRequest};
use super::service;
use crate::error::AppError;
use crate::state::AppState;

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

fn client_ip(addr: Option<ConnectInfo<SocketAddr>>) -> String {
    addr.map(|a| a.0.ip().to_string())
        .unwrap_or_else(|| "unknown".to_string())
}

async fn get_profile(
    State(state): State<AppState>,
    headers: HeaderMap,
) -> Result<Json<ProfileResponse>, AppError> {
    let token = extract_bearer(&headers)?;
    let profile = service::get_profile(&state, token).await?;
    Ok(Json(profile))
}

async fn update_profile(
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

async fn list_sessions(
    State(state): State<AppState>,
    headers: HeaderMap,
) -> Result<Json<SessionListResponse>, AppError> {
    let token = extract_bearer(&headers)?;
    let list = service::list_sessions(&state, token).await?;
    Ok(Json(list))
}

async fn revoke_one(
    State(state): State<AppState>,
    headers: HeaderMap,
    Path(id): Path<Uuid>,
) -> Result<Json<MessageResponse>, AppError> {
    let token = extract_bearer(&headers)?;
    let message = service::revoke_session(&state, token, id).await?;
    Ok(Json(MessageResponse { message }))
}

async fn revoke_others(
    State(state): State<AppState>,
    headers: HeaderMap,
) -> Result<Json<MessageResponse>, AppError> {
    let token = extract_bearer(&headers)?;
    let message = service::revoke_other_sessions(&state, token).await?;
    Ok(Json(MessageResponse { message }))
}

async fn delete_account(
    State(state): State<AppState>,
    addr: Option<ConnectInfo<SocketAddr>>,
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
    use axum::extract::ConnectInfo;
    use std::net::{IpAddr, Ipv6Addr, SocketAddr};

    #[test]
    fn client_ip_uses_only_the_connection_peer() {
        let peer = SocketAddr::new(IpAddr::V6(Ipv6Addr::LOCALHOST), 8080);
        assert_eq!(client_ip(Some(ConnectInfo(peer))), "::1");
        assert_eq!(client_ip(None), "unknown");
    }
}
