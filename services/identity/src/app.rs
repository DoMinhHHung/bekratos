use axum::extract::State;
use axum::http::StatusCode;
use axum::response::IntoResponse;
use axum::routing::get;
use axum::{Json, Router};
use serde::Serialize;
use tower_http::limit::RequestBodyLimitLayer;
use tower_http::timeout::TimeoutLayer;
use tower_http::trace::TraceLayer;

use crate::error::AppError;
use crate::infra::db;
use crate::infra::redis as redis_infra;
use crate::modules::auth;
use crate::modules::jwks;
use crate::modules::user;
use crate::state::AppState;

pub fn build_router(state: AppState) -> Router {
    let body_limit = state.config.body_limit_bytes;
    let timeout = state.config.request_timeout;

    Router::new()
        .route("/healthz", get(healthz))
        .route("/readyz", get(readyz))
        .merge(crate::openapi::swagger_ui())
        .merge(auth::routes::router())
        .merge(crate::modules::internal::router())
        .merge(user::routes::router())
        .merge(jwks::routes::router())
        .layer(TraceLayer::new_for_http())
        .layer(TimeoutLayer::with_status_code(
            StatusCode::REQUEST_TIMEOUT,
            timeout,
        ))
        .layer(RequestBodyLimitLayer::new(body_limit))
        .with_state(state)
}

#[derive(Serialize, utoipa::ToSchema)]
pub(crate) struct HealthResponse {
    status: String,
}

#[utoipa::path(
    get,
    path = "/healthz",
    responses((status = 200, description = "Service is alive", body = HealthResponse)),
    tag = "Health"
)]
pub(crate) async fn healthz() -> impl IntoResponse {
    (
        StatusCode::OK,
        Json(HealthResponse {
            status: "ok".into(),
        }),
    )
}

#[derive(Serialize, utoipa::ToSchema)]
pub(crate) struct ReadyResponse {
    status: String,
}

#[utoipa::path(
    get,
    path = "/readyz",
    responses(
        (status = 200, description = "Service dependencies are ready", body = ReadyResponse),
        (status = 503, description = "A required dependency is unavailable")
    ),
    tag = "Health"
)]
pub(crate) async fn readyz(State(state): State<AppState>) -> Result<impl IntoResponse, AppError> {
    if state.config.readiness_require_db {
        db::ping(&state.db).await?;
    }

    if state.config.readiness_require_redis {
        let mut conn = state.redis.clone();
        redis_infra::ping(&mut conn).await?;
    }

    Ok((
        StatusCode::OK,
        Json(ReadyResponse {
            status: "ready".into(),
        }),
    ))
}
