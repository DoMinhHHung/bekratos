use axum::extract::State;
use axum::routing::get;
use axum::{Json, Router};
use serde_json::Value as JsonValue;

use super::service;
use crate::error::AppError;
use crate::state::AppState;

pub fn router() -> Router<AppState> {
    Router::new().route("/v1/jwks", get(jwks))
}

#[utoipa::path(
    get,
    path = "/v1/jwks",
    responses((status = 200, description = "Public signing keys", body = crate::openapi::JwksDocument)),
    tag = "JWKS"
)]
pub(crate) async fn jwks(State(state): State<AppState>) -> Result<Json<JsonValue>, AppError> {
    let body = service::get_jwks(&state).await?;
    Ok(Json(body))
}
