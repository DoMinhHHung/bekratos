use chrono::{DateTime, Utc};
use sqlx::PgPool;
use sqlx::{Postgres, Transaction};
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

    pub async fn find_by_email(&self, email: &str) -> AppResult<Option<(UserRow, UserEmailRow)>> {
        let email_row = sqlx::query_as::<_, UserEmailRow>(
            r#"
        SELECT id, user_id, email, is_primary, verified_at, created_at, updated_at
        FROM "user".user_emails
        WHERE email = $1
        "#,
        )
        .bind(email)
        .fetch_optional(self.pool)
        .await
        .map_err(|e| {
            tracing::error!(error = %e, "find email failed");
            AppError::Internal
        })?;

        let Some(email_row) = email_row else {
            return Ok(None);
        };

        let user = self.find_by_id(email_row.user_id).await?;
        Ok(user.map(|u| (u, email_row)))
    }

    pub async fn find_by_username(&self, username: &str) -> AppResult<Option<UserRow>> {
        let row = sqlx::query_as::<_, UserRow>(
            r#"
            SELECT
                u.id,
                u.status,
                u.status_before_deletion,
                u.locale,
                u.timezone,
                u.deletion_requested_at,
                u.purge_after,
                u.created_at,
                u.updated_at
            FROM "user".user_usernames un
            INNER JOIN "user".users u ON u.id = un.user_id
            WHERE un.username = $1
            "#,
        )
        .bind(username)
        .fetch_optional(self.pool)
        .await
        .map_err(|e| {
            tracing::error!(error = %e, "find user by username failed");
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

    pub async fn insert_pending_tx(
        tx: &mut Transaction<'_, Postgres>,
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
        .fetch_one(&mut **tx)
        .await
        .map_err(|e| map_db_err(e, "insert pending user tx failed"))?;
        Ok(row)
    }

    pub async fn insert_profile_tx(
        tx: &mut Transaction<'_, Postgres>,
        user_id: Uuid,
        full_name: Option<&str>,
        display_name: Option<&str>,
    ) -> AppResult<()> {
        sqlx::query(
            r#"
            INSERT INTO "user".user_profile (user_id, full_name, display_name)
            VALUES ($1, $2, $3)
            "#,
        )
        .bind(user_id)
        .bind(full_name)
        .bind(display_name)
        .execute(&mut **tx)
        .await
        .map_err(|e| {
            tracing::error!(error = %e, %user_id, "insert user profile failed");
            AppError::Internal
        })?;
        Ok(())
    }

    pub async fn insert_email_tx(
        tx: &mut Transaction<'_, Postgres>,
        id: Uuid,
        user_id: Uuid,
        email: &str,
        is_primary: bool,
    ) -> AppResult<()> {
        sqlx::query(
            r#"
            INSERT INTO "user".user_emails (id, user_id, email, is_primary)
            VALUES ($1, $2, $3, $4)
            "#,
        )
        .bind(id)
        .bind(user_id)
        .bind(email)
        .bind(is_primary)
        .execute(&mut **tx)
        .await
        .map_err(|e| map_db_err(e, "insert user email failed"))?;
        Ok(())
    }

    pub async fn insert_username_tx(
        tx: &mut Transaction<'_, Postgres>,
        id: Uuid,
        user_id: Uuid,
        username: &str,
    ) -> AppResult<()> {
        sqlx::query(
            r#"
            INSERT INTO "user".user_usernames (id, user_id, username)
            VALUES ($1, $2, $3)
            "#,
        )
        .bind(id)
        .bind(user_id)
        .bind(username)
        .execute(&mut **tx)
        .await
        .map_err(|e| map_db_err(e, "insert username failed"))?;
        Ok(())
    }

    pub async fn insert_policy_acceptance_tx(
        tx: &mut Transaction<'_, Postgres>,
        input: NewPolicyAcceptance<'_>,
    ) -> AppResult<()> {
        sqlx::query(
            r#"
            INSERT INTO "user".policy_acceptances (
                id, user_id, policy_type, version, document_sha256, ip, user_agent
            )
            VALUES ($1, $2, $3, $4, $5, $6, $7)
            "#,
        )
        .bind(input.id)
        .bind(input.user_id)
        .bind(input.policy_type)
        .bind(input.version)
        .bind(input.document_sha256)
        .bind(input.ip)
        .bind(input.user_agent)
        .execute(&mut **tx)
        .await
        .map_err(|e| {
            tracing::error!(error = %e, user_id = %input.user_id, "insert policy acceptance failed");
            AppError::Internal
        })?;
        Ok(())
    }

    pub async fn activate_verified_email_tx(
        tx: &mut Transaction<'_, Postgres>,
        user_id: Uuid,
        email: &str,
    ) -> AppResult<()> {
        let user_result = sqlx::query(
            r#"
            UPDATE "user".users
            SET status = 'active', updated_at = now()
            WHERE id = $1 AND status = 'pending'
            "#,
        )
        .bind(user_id)
        .execute(&mut **tx)
        .await
        .map_err(|e| {
            tracing::error!(error = %e, %user_id, "activate user failed");
            AppError::Internal
        })?;

        if user_result.rows_affected() != 1 {
            return Err(AppError::BadRequest("invalid token".into()));
        }

        let email_result = sqlx::query(
            r#"
            UPDATE "user".user_emails
            SET verified_at = now(), updated_at = now()
            WHERE user_id = $1 AND email = $2 AND verified_at IS NULL
            "#,
        )
        .bind(user_id)
        .bind(email)
        .execute(&mut **tx)
        .await
        .map_err(|e| {
            tracing::error!(error = %e, %user_id, "verify email failed");
            AppError::Internal
        })?;

        if email_result.rows_affected() != 1 {
            return Err(AppError::BadRequest("invalid token".into()));
        }

        Ok(())
    }
}

pub fn is_unique_violation(err: &sqlx::Error) -> bool {
    match err {
        sqlx::Error::Database(db) => db.code().as_deref() == Some("23505"),
        _ => false,
    }
}

fn map_db_err(err: sqlx::Error, context: &str) -> AppError {
    if is_unique_violation(&err) {
        tracing::debug!(error = %err, "{context} unique violation");
        return AppError::Conflict;
    }
    tracing::error!(error = %err, "{context}");
    AppError::Internal
}
