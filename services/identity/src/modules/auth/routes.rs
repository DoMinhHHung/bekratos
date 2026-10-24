use std::net::SocketAddr;

use axum::extract::{ConnectInfo, State};
use axum::routing::post;
use axum::{Json, Router};
use validator::Validate;

use super::dto::{RegisterRequest, RegisterResponse};
use super::service;
use crate::error::AppError;
use crate::state::AppState;

pub fn router() -> Router<AppState> {
    Router::new().route("/v1/auth/register", post(register))
}

fn client_ip(addr: Option<ConnectInfo<SocketAddr>>) -> String {
    addr.map(|a| a.0.ip().to_string())
        .unwrap_or_else(|| "unknown".to_string())
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
