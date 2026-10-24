use serde::{Deserialize, Serialize};
use validator::Validate;

#[derive(Debug, Deserialize, Validate)]
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

#[derive(Debug, Serialize)]
pub struct RegisterResponse {
    pub message: String,
}
