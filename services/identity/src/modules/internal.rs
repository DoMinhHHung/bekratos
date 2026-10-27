use axum::extract::State;
use axum::http::{header, HeaderMap, StatusCode};
use axum::response::{IntoResponse, Response};
use axum::routing::post;
use axum::{Json, Router};
use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use subtle::ConstantTimeEq;
use uuid::Uuid;

use crate::state::AppState;

pub fn router() -> Router<AppState> {
    Router::new().route("/internal/v1/identity/introspect", post(introspect))
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct IntrospectionRequest {
    access_token: String,
}

#[derive(Serialize)]
struct IntrospectionResponse {
    active: bool,
    user_id: String,
    session_id: String,
    issuer: String,
    audience: String,
    expires_at: i64,
    email_verified: bool,
    verified_primary_email: Option<String>,
}

#[derive(Serialize)]
struct ErrorResponse {
    error: &'static str,
    message: &'static str,
}

#[derive(sqlx::FromRow)]
struct SessionIdentityRow {
    user_id: Uuid,
    session_id: Uuid,
    session_expires_at: DateTime<Utc>,
    session_revoked_at: Option<DateTime<Utc>>,
    account_status: String,
    verified_primary_email: Option<String>,
}

async fn introspect(
    State(state): State<AppState>,
    headers: HeaderMap,
    Json(request): Json<IntrospectionRequest>,
) -> Response {
    let Some(expected_token) = state.config.identity_service_api_token.as_deref() else {
        return error_response(
            StatusCode::SERVICE_UNAVAILABLE,
            "service_contract_disabled",
            "identity service contract is not configured",
        );
    };
    let Some(supplied_token) = headers
        .get("x-identity-service-token")
        .map(|value| value.as_bytes())
    else {
        return unauthorized();
    };
    if supplied_token.ct_eq(expected_token.as_bytes()).unwrap_u8() != 1 {
        return unauthorized();
    }
    if request.access_token.is_empty() || request.access_token.len() > 16 * 1024 {
        return error_response(
            StatusCode::BAD_REQUEST,
            "invalid_request",
            "invalid introspection request",
        );
    }

    let claims = match state
        .jwt_verifier
        .verify_access_token(&request.access_token)
    {
        Ok(claims) => claims,
        Err(_) => return unauthorized(),
    };
    let Ok(user_id) = Uuid::parse_str(&claims.sub) else {
        return unauthorized();
    };
    let Ok(session_id) = Uuid::parse_str(&claims.sid) else {
        return unauthorized();
    };

    let row = sqlx::query_as::<_, SessionIdentityRow>(
        r#"
        SELECT
            u.id AS user_id,
            s.id AS session_id,
            s.expires_at AS session_expires_at,
            s.revoked_at AS session_revoked_at,
            u.status AS account_status,
            e.email::TEXT AS verified_primary_email
        FROM session.sessions s
        JOIN "user".users u ON u.id = s.user_id
        LEFT JOIN "user".user_emails e
            ON e.user_id = u.id
           AND e.is_primary = TRUE
           AND e.verified_at IS NOT NULL
        WHERE s.id = $1 AND s.user_id = $2
        "#,
    )
    .bind(session_id)
    .bind(user_id)
    .fetch_optional(&state.db)
    .await;

    let row = match row {
        Ok(row) => row,
        Err(error) => {
            tracing::error!(error = %error, "identity introspection query failed");
            return error_response(
                StatusCode::SERVICE_UNAVAILABLE,
                "identity_unavailable",
                "identity state is temporarily unavailable",
            );
        }
    };

    let now = Utc::now();
    let (active, verified_primary_email) = match row {
        Some(row) => {
            let active = row.user_id == user_id
                && row.session_id == session_id
                && row.session_revoked_at.is_none()
                && row.session_expires_at > now
                && row.account_status == "active"
                && claims.exp > now.timestamp();
            let email = if active {
                row.verified_primary_email
            } else {
                None
            };
            (active, email)
        }
        None => (false, None),
    };

    let response = IntrospectionResponse {
        active,
        user_id: claims.sub,
        session_id: claims.sid,
        issuer: claims.iss,
        audience: claims.aud,
        expires_at: claims.exp,
        email_verified: verified_primary_email.is_some(),
        verified_primary_email,
    };
    let mut response = Json(response).into_response();
    response.headers_mut().insert(
        header::CACHE_CONTROL,
        "no-store".parse().expect("static header"),
    );
    response
}

fn unauthorized() -> Response {
    error_response(
        StatusCode::UNAUTHORIZED,
        "unauthorized",
        "invalid service credentials or access token",
    )
}

fn error_response(status: StatusCode, code: &'static str, message: &'static str) -> Response {
    let mut response = (
        status,
        Json(ErrorResponse {
            error: code,
            message,
        }),
    )
        .into_response();
    response.headers_mut().insert(
        header::CACHE_CONTROL,
        "no-store".parse().expect("static header"),
    );
    response
}
