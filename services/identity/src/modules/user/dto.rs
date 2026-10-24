use serde::{Deserialize, Serialize};
use utoipa::ToSchema;
use uuid::Uuid;
use validator::Validate;

#[derive(Debug, Serialize, ToSchema)]
pub struct ProfileResponse {
    pub id: Uuid,
    pub status: String,
    pub email: Option<String>,
    pub username: Option<String>,
    pub full_name: Option<String>,
    pub display_name: Option<String>,
    pub avatar_url: Option<String>,
    pub locale: Option<String>,
    pub timezone: Option<String>,
}

#[derive(Debug, Deserialize, ToSchema, Validate)]
pub struct UpdateProfileRequest {
    #[validate(length(max = 120))]
    pub full_name: Option<String>,
    #[validate(length(max = 60))]
    pub display_name: Option<String>,
    #[validate(length(max = 512))]
    pub avatar_url: Option<String>,
    #[validate(length(max = 16))]
    pub locale: Option<String>,
    #[validate(length(max = 64))]
    pub timezone: Option<String>,
}

#[derive(Debug, Serialize, ToSchema)]
pub struct SessionItem {
    pub id: Uuid,
    pub ip: Option<String>,
    pub user_agent: Option<String>,
    pub device_name: Option<String>,
    pub last_seen_at: chrono::DateTime<chrono::Utc>,
    pub expires_at: chrono::DateTime<chrono::Utc>,
    pub created_at: chrono::DateTime<chrono::Utc>,
    pub is_current: bool,
}

#[derive(Debug, Serialize, ToSchema)]
pub struct SessionListResponse {
    pub sessions: Vec<SessionItem>,
}

#[derive(Debug, Serialize, ToSchema)]
pub struct MessageResponse {
    pub message: String,
}
