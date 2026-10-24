use chrono::{DateTime, Utc};
use sqlx::PgPool;
use uuid::Uuid;

use crate::error::{AppError, AppResult};

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

    pub async fn insert_pending(
        &self,
        id: Uuid,
        locale: Option<&str>,
        timezone: Option<&str>,
    ) -> AppResult<UserRow> {
        let row = sqlx::query_as::<_, UserRow>(
            r#"
            INSERT INTO "user".users (id, status, locale, timezone)
            VALUES ($1, 'pending', $2, $3)
            RETURNING
                id,
                status,
                status_before_deletion,
                locale,
                timezone,
                deletion_requested_at,
                purge_after,
                created_at,
                updated_at
            "#,
        )
        .bind(id)
        .bind(locale)
        .bind(timezone)
        .fetch_one(self.pool)
        .await
        .map_err(|e| {
            tracing::error!(error = %e, %id, "insert pending user failed");
            AppError::Internal
        })?;

        Ok(row)
    }
}
