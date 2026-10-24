use axum::http::{header::CONTENT_TYPE, StatusCode};
use axum::response::IntoResponse;
use http_body_util::BodyExt;
use identity::AppError;
use serde_json::{json, Value};

#[tokio::test]
async fn every_application_error_has_a_stable_http_status_and_json_body() {
    for (error, status, code, message) in [
        (
            AppError::config("sensitive configuration detail"),
            StatusCode::INTERNAL_SERVER_ERROR,
            "config_error",
            "internal server error",
        ),
        (
            AppError::Internal,
            StatusCode::INTERNAL_SERVER_ERROR,
            "internal_error",
            "internal server error",
        ),
        (
            AppError::Unavailable,
            StatusCode::SERVICE_UNAVAILABLE,
            "service_unavailable",
            "service unavailable",
        ),
        (
            AppError::NotFound,
            StatusCode::NOT_FOUND,
            "not_found",
            "not found",
        ),
        (
            AppError::BadRequest("invalid input".into()),
            StatusCode::BAD_REQUEST,
            "bad_request",
            "invalid input",
        ),
        (
            AppError::TooManyRequests,
            StatusCode::TOO_MANY_REQUESTS,
            "too_many_requests",
            "too many requests",
        ),
        (
            AppError::Conflict,
            StatusCode::CONFLICT,
            "conflict",
            "conflict",
        ),
        (
            AppError::Unauthorized,
            StatusCode::UNAUTHORIZED,
            "unauthorized",
            "unauthorized",
        ),
    ] {
        assert_eq!(error.status_code(), status);
        assert_eq!(error.code(), code);
        assert_eq!(error.public_message(), message);
        let response = error.into_response();
        assert_eq!(response.status(), status);
        assert_eq!(response.headers()[CONTENT_TYPE], "application/json");
        let body = response.into_body().collect().await.unwrap().to_bytes();
        let body: Value = serde_json::from_slice(&body).unwrap();
        assert_eq!(body, json!({"code": code, "message": message}));
    }
}

#[tokio::test]
async fn bad_request_messages_are_json_escaped_without_losing_unicode() {
    let message = "invalid \"display_name\"\nUse a name like Nguyễn";
    let response = AppError::BadRequest(message.into()).into_response();
    assert_eq!(response.status(), StatusCode::BAD_REQUEST);
    let body = response.into_body().collect().await.unwrap().to_bytes();
    let body: Value = serde_json::from_slice(&body).unwrap();
    assert_eq!(body, json!({"code": "bad_request", "message": message}));
}
