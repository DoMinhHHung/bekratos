use axum::extract::State;
use axum::routing::get;
use axum::{Json, Router};
use serde_json::Value as JsonValue;

use super::service;
use crate::error::AppError;
use crate::state::AppState;

/// Tạo route GET `/v1/jwks` để công bố khóa công khai, không yêu cầu Bearer token.
pub fn router() -> Router<AppState> {
    Router::new().route("/v1/jwks", get(jwks))
}

/// Trả tập khóa công khai dưới dạng JSON; truyền lỗi cơ sở dữ liệu `Internal` từ service.
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
