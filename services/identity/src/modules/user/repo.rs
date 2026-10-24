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

#[derive(Debug, Clone, sqlx::FromRow)]
pub struct UserEmailRow {
    pub id: Uuid,
    pub user_id: Uuid,
    pub email: String,
    pub is_primary: bool,
    pub verified_at: Option<DateTime<Utc>>,
    pub created_at: DateTime<Utc>,
    pub updated_at: DateTime<Utc>,
}

pub struct NewPolicyAcceptance<'a> {
    pub id: Uuid,
    pub user_id: Uuid,
    pub policy_type: &'a str,
    pub version: &'a str,
    pub document_sha256: &'a str,
    pub ip: Option<&'a str>,
    pub user_agent: Option<&'a str>,
}

pub struct UserRepo<'a> {
    pool: &'a PgPool,
}

impl<'a> UserRepo<'a> {
    pub fn new(pool: &'a PgPool) -> Self {
        Self { pool }
    }

    pub async fn find_by_id(&self, id: Uuid) -> AppResult<Option<UserRow>> {
        let row = sqlx::query_as::<_, UserRow>(
            r#"
            SELECT
                id,
                status,
                status_before_deletion,
                locale,
                timezone,
                deletion_requested_at,
                purge_after,
                created_at,
                updated_at
            FROM "user".users
            WHERE id = $1
            "#,
        )
        .bind(id)
        .fetch_optional(self.pool)
        .await
        .map_err(|e| {
            tracing::error!(error = %e, %id, "find user by id failed");
            AppError::Internal
        })?;
        Ok(row)
    }
