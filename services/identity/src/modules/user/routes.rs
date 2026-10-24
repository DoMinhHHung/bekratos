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
