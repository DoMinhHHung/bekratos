use serde::{Deserialize, Serialize};
use utoipa::ToSchema;
use validator::Validate;

#[derive(Debug, Deserialize, ToSchema, Validate)]
pub struct RegisterRequest {
    #[validate(email(message = "invalid email"))]
    pub email: String,
    #[validate(length(min = 3, max = 30, message = "invalid username"))]
    pub username: String,
    #[validate(length(min = 8, max = 128, message = "invalid password"))]
    pub password: String,
    pub accept_policy: bool,
    pub locale: Option<String>,
    pub timezone: Option<String>,
    pub full_name: Option<String>,
}

#[derive(Debug, Serialize, ToSchema)]
pub struct RegisterResponse {
    pub message: String,
}

#[derive(Debug, Deserialize, ToSchema, Validate)]
pub struct VerifyEmailRequest {
    #[validate(length(min = 10, max = 512, message = "invalid token"))]
    pub token: String,
}

#[derive(Debug, Serialize, ToSchema)]
pub struct VerifyEmailResponse {
    pub message: String,
}

#[derive(Debug, Deserialize, ToSchema, Validate)]
pub struct ResendVerificationRequest {
    #[validate(email(message = "invalid email"))]
    pub email: String,
}

#[derive(Debug, Serialize, ToSchema)]
pub struct ResendVerificationResponse {
    pub message: String,
}

#[derive(Debug, Deserialize, ToSchema, Validate)]
pub struct LoginRequest {
    #[validate(length(min = 3, max = 254, message = "invalid login"))]
    pub login: String,
    #[validate(length(min = 8, max = 128, message = "invalid password"))]
    pub password: String,
    pub device_name: Option<String>,
}

#[derive(Debug, Serialize, ToSchema)]
pub struct LoginResponse {
    pub access_token: String,
    pub refresh_token: String,
    pub token_type: String,
    pub expires_in: i64,
}

#[derive(Debug, Serialize, ToSchema)]
pub struct LogoutResponse {
    pub message: String,
}

#[derive(Debug, Deserialize, ToSchema, Validate)]
pub struct RefreshRequest {
    #[validate(length(min = 32, max = 128, message = "invalid refresh token"))]
    pub refresh_token: String,
}

#[derive(Debug, Serialize, ToSchema)]
pub struct RefreshResponse {
    pub access_token: String,
    pub refresh_token: String,
    pub token_type: String,
    pub expires_in: i64,
}

#[derive(Debug, Deserialize, ToSchema, Validate)]
pub struct ForgotPasswordRequest {
    #[validate(email(message = "invalid email"))]
    pub email: String,
}

#[derive(Debug, Serialize, ToSchema)]
pub struct ForgotPasswordResponse {
    pub message: String,
}

#[derive(Debug, Deserialize, ToSchema, Validate)]
pub struct ResetPasswordRequest {
    #[validate(length(min = 10, max = 512, message = "invalid token"))]
    pub token: String,
    #[validate(length(min = 8, max = 128, message = "invalid password"))]
    pub new_password: String,
}

#[derive(Debug, Serialize, ToSchema)]
pub struct ResetPasswordResponse {
    pub message: String,
}

#[derive(Debug, Deserialize, ToSchema, Validate)]
pub struct ChangePasswordRequest {
    #[validate(length(min = 8, max = 128, message = "invalid password"))]
    pub current_password: String,
    #[validate(length(min = 8, max = 128, message = "invalid password"))]
    pub new_password: String,
}

#[derive(Debug, Serialize, ToSchema)]
pub struct ChangePasswordResponse {
    pub message: String,
}

#[derive(Debug, Deserialize, ToSchema, Validate)]
pub struct RestoreRequestBody {
    #[validate(email)]
    pub email: String,
}

#[derive(Debug, Deserialize, ToSchema, Validate)]
pub struct RestoreConfirmRequest {
    #[validate(length(min = 10, max = 512))]
    pub token: String,
}

#[derive(Debug, Serialize, ToSchema)]
pub struct RestoreResponse {
    pub message: String,
}
