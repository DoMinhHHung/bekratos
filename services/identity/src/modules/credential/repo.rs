use sqlx::{PgPool, Postgres, Transaction};
use uuid::Uuid;

use crate::error::{AppError, AppResult};

pub struct CredentialRepo;

impl CredentialRepo {
    pub async fn insert_password_tx(
        tx: &mut Transaction<'_, Postgres>,
        user_id: Uuid,
        password_hash: &str,
    ) -> AppResult<()> {
        sqlx::query(
            r#"
            INSERT INTO credential.password_credentials (user_id, password_hash)
            VALUES ($1, $2)
            "#,
        )
        .bind(user_id)
        .bind(password_hash)
        .execute(&mut **tx)
        .await
        .map_err(|e| {
            tracing::error!(error = %e, %user_id, "insert password credential failed");
            AppError::Internal
        })?;
        Ok(())
    }

    pub async fn insert_history_tx(
        tx: &mut Transaction<'_, Postgres>,
        id: Uuid,
        user_id: Uuid,
        password_hash: &str,
    ) -> AppResult<()> {
        sqlx::query(
            r#"
            INSERT INTO credential.password_history (id, user_id, password_hash)
            VALUES ($1, $2, $3)
            "#,
        )
        .bind(id)
        .bind(user_id)
        .bind(password_hash)
        .execute(&mut **tx)
        .await
        .map_err(|e| {
            tracing::error!(error = %e, %user_id, "insert password history failed");
            AppError::Internal
        })?;
        Ok(())
    }

    pub async fn find_password_hash(pool: &PgPool, user_id: Uuid) -> AppResult<Option<String>> {
        let row: Option<(String,)> = sqlx::query_as(
            r#"
            SELECT password_hash
            FROM credential.password_credentials
            WHERE user_id = $1
            "#,
        )
        .bind(user_id)
        .fetch_optional(pool)
        .await
        .map_err(|e| {
            tracing::error!(error = %e, %user_id, "find password hash failed");
            AppError::Internal
        })?;
        Ok(row.map(|r| r.0))
    }
}
