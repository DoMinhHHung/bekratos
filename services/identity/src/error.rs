use axum::http::StatusCode;
use axum::response::{IntoResponse, Response};
use axum::Json;
use serde::Serialize;
use thiserror::Error;

pub type AppResult<T> = Result<T, AppError>;

#[derive(Debug, Error)]
pub enum AppError {
    #[error("configuration error: {0}")]
    Config(String),

    #[error("internal error")]
    Internal,

    #[error("service unavailable")]
    Unavailable,

    #[error("not found")]
    NotFound,

    #[error("bad request")]
    BadRequest(String),

    #[error("too many requests")]
    TooManyRequests,

    #[error("conflict")]
    Conflict,

    #[error("unauthorized")]
    Unauthorized,
}

impl AppError {
    /// Tạo lỗi cấu hình với chi tiết dùng nội bộ; phản hồi HTTP sẽ che chi tiết này.
    pub fn config(msg: impl Into<String>) -> Self {
        Self::Config(msg.into())
    }

    /// Ánh xạ lỗi sang trạng thái HTTP, trong đó lỗi cấu hình và lỗi nội bộ đều là 500.
    pub fn status_code(&self) -> StatusCode {
        match self {
            Self::Config(_) => StatusCode::INTERNAL_SERVER_ERROR,
            Self::Internal => StatusCode::INTERNAL_SERVER_ERROR,
            Self::Unavailable => StatusCode::SERVICE_UNAVAILABLE,
            Self::NotFound => StatusCode::NOT_FOUND,
            Self::BadRequest(_) => StatusCode::BAD_REQUEST,
            Self::TooManyRequests => StatusCode::TOO_MANY_REQUESTS,
            Self::Conflict => StatusCode::CONFLICT,
            Self::Unauthorized => StatusCode::UNAUTHORIZED,
        }
    }

    /// Trả mã lỗi dạng snake_case dùng trong trường `code` của phản hồi API.
    pub fn code(&self) -> &'static str {
        match self {
            Self::Config(_) => "config_error",
            Self::Internal => "internal_error",
            Self::Unavailable => "service_unavailable",
            Self::NotFound => "not_found",
            Self::BadRequest(_) => "bad_request",
            Self::TooManyRequests => "too_many_requests",
            Self::Conflict => "conflict",
            Self::Unauthorized => "unauthorized",
        }
    }

    /// Trả thông điệp cho client, che chi tiết cấu hình và lỗi nội bộ.
    /// Thông điệp `BadRequest` được giữ nguyên.
    pub fn public_message(&self) -> String {
        match self {
            Self::Config(_) => "internal server error".to_string(),
            Self::Internal => "internal server error".to_string(),
            Self::Unavailable => "service unavailable".to_string(),
            Self::NotFound => "not found".to_string(),
            Self::BadRequest(msg) => msg.clone(),
            Self::TooManyRequests => "too many requests".to_string(),
            Self::Conflict => "conflict".to_string(),
            Self::Unauthorized => "unauthorized".to_string(),
        }
    }
}

#[derive(Serialize)]
struct ErrorBody {
    code: &'static str,
    message: String,
}

impl IntoResponse for AppError {
    /// Tạo phản hồi JSON chứa `code` và thông điệp công khai với trạng thái HTTP tương ứng.
    fn into_response(self) -> Response {
        let status = self.status_code();
        let body = ErrorBody {
            code: self.code(),
            message: self.public_message(),
        };
        (status, Json(body)).into_response()
    }
}
