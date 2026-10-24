use chrono::{DateTime, Utc};
use sqlx::PgPool;
use sqlx::{Postgres, Transaction};
use uuid::Uuid;

use crate::error::{AppError, AppResult};

type ProfileRow = (String, Option<String>, Option<String>, Option<String>);

#[derive(Debug, Clone)]
pub struct ProfileBundle {
    pub user: UserRow,
    pub full_name: Option<String>,
    pub display_name: Option<String>,
    pub avatar_url: Option<String>,
    pub email: Option<String>,
    pub username: Option<String>,
}

#[derive(Debug, Clone, sqlx::FromRow)]
pub struct UserRow {
    pub id: Uuid,
    pub status: String,
    pub status_before_deletion: Option<String>,
    pub locale: Option<String>,
    pub timezone: Option<String>,
    pub deletion_requested_at: Option<DateTime<Utc>>,
    pub purge_after: Option<DateTime<Utc>>,
    pub created_at: DateTime<Utc>,
    pub updated_at: DateTime<Utc>,
}
