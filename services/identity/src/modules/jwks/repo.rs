use chrono::{DateTime, Utc};
use serde_json::Value as JsonValue;
use sqlx::{PgPool, Postgres, Transaction};
use uuid::Uuid;

use crate::error::{AppError, AppResult};

#[derive(Debug, Clone, sqlx::FromRow)]
pub struct SigningKeyRow {
    pub id: Uuid,
    pub kid: String,
    pub alg: String,
    pub public_jwk: JsonValue,
    pub private_key_enc: Vec<u8>,
    pub key_version: i32,
    pub status: String,
    pub publish_at: Option<DateTime<Utc>>,
    pub activated_at: Option<DateTime<Utc>>,
    pub retire_at: Option<DateTime<Utc>>,
    pub publish_until: Option<DateTime<Utc>>,
    pub created_at: DateTime<Utc>,
}

pub struct NewSigningKey {
    pub id: Uuid,
    pub kid: String,
    pub public_jwk: JsonValue,
    pub private_key_enc: Vec<u8>,
    pub key_version: i32,
    pub status: String,
    pub publish_at: Option<DateTime<Utc>>,
}

pub struct JwksRepo;

impl JwksRepo {
    pub async fn insert_tx(
        tx: &mut Transaction<'_, Postgres>,
        key: NewSigningKey,
    ) -> AppResult<()> {
        sqlx::query(
            r#"
            INSERT INTO jwks.signing_keys (
                id, kid, alg, public_jwk, private_key_enc, key_version, status, publish_at
            )
            VALUES ($1, $2, 'ES256', $3, $4, $5, $6, $7)
            "#,
        )
        .bind(key.id)
        .bind(key.kid)
        .bind(key.public_jwk)
        .bind(key.private_key_enc)
        .bind(key.key_version)
        .bind(key.status)
        .bind(key.publish_at)
        .execute(&mut **tx)
        .await
        .map_err(|e| {
            tracing::error!(error = %e, "insert signing key failed");
            AppError::Internal
        })?;
        Ok(())
    }

    pub async fn find_active(pool: &PgPool) -> AppResult<Option<SigningKeyRow>> {
        sqlx::query_as::<_, SigningKeyRow>(
            r#"
            SELECT
                id, kid, alg, public_jwk, private_key_enc, key_version, status,
                publish_at, activated_at, retire_at, publish_until, created_at
            FROM jwks.signing_keys
            WHERE status = 'active'
            LIMIT 1
            "#,
        )
        .fetch_optional(pool)
        .await
        .map_err(|e| {
            tracing::error!(error = %e, "find active signing key failed");
            AppError::Internal
        })
    }

    pub async fn find_by_kid(pool: &PgPool, kid: &str) -> AppResult<Option<SigningKeyRow>> {
        sqlx::query_as::<_, SigningKeyRow>(
            r#"
            SELECT
                id, kid, alg, public_jwk, private_key_enc, key_version, status,
                publish_at, activated_at, retire_at, publish_until, created_at
            FROM jwks.signing_keys
            WHERE kid = $1
            "#,
        )
        .bind(kid)
        .fetch_optional(pool)
        .await
        .map_err(|e| {
            tracing::error!(error = %e, "find signing key by kid failed");
            AppError::Internal
        })
    }

    pub async fn list_publishable(pool: &PgPool) -> AppResult<Vec<SigningKeyRow>> {
        sqlx::query_as::<_, SigningKeyRow>(
            r#"
            SELECT
                id, kid, alg, public_jwk, private_key_enc, key_version, status,
                publish_at, activated_at, retire_at, publish_until, created_at
            FROM jwks.signing_keys
            WHERE status IN ('active', 'retiring')
              AND (publish_until IS NULL OR publish_until > now())
            ORDER BY key_version DESC
            "#,
        )
        .fetch_all(pool)
        .await
        .map_err(|e| {
            tracing::error!(error = %e, "list publishable keys failed");
            AppError::Internal
        })
    }

    pub async fn activate_tx(tx: &mut Transaction<'_, Postgres>, id: Uuid) -> AppResult<()> {
        sqlx::query(
            r#"
            UPDATE jwks.signing_keys
            SET status = 'retiring',
                retire_at = now(),
                publish_until = now() + interval '1 hour'
            WHERE status = 'active'
            "#,
        )
        .execute(&mut **tx)
        .await
        .map_err(|e| {
            tracing::error!(error = %e, "retire previous active key failed");
            AppError::Internal
        })?;

        let res = sqlx::query(
            r#"
            UPDATE jwks.signing_keys
            SET status = 'active',
                activated_at = now(),
                publish_at = COALESCE(publish_at, now())
            WHERE id = $1 AND status IN ('pending', 'retiring')
            "#,
        )
        .bind(id)
        .execute(&mut **tx)
        .await
        .map_err(|e| {
            tracing::error!(error = %e, "activate signing key failed");
            AppError::Internal
        })?;

        if res.rows_affected() != 1 {
            return Err(AppError::Conflict);
        }
        Ok(())
    }

    pub async fn mark_retired_expired(pool: &PgPool) -> AppResult<u64> {
        let res = sqlx::query(
            r#"
            UPDATE jwks.signing_keys
            SET status = 'retired'
            WHERE status = 'retiring'
              AND publish_until IS NOT NULL
              AND publish_until <= now()
            "#,
        )
        .execute(pool)
        .await
        .map_err(|e| {
            tracing::error!(error = %e, "mark retired keys failed");
            AppError::Internal
        })?;
        Ok(res.rows_affected())
    }

    pub async fn next_version(pool: &PgPool) -> AppResult<i32> {
        let row: (Option<i32>,) = sqlx::query_as(
            r#"
            SELECT MAX(key_version) FROM jwks.signing_keys
            "#,
        )
        .fetch_one(pool)
        .await
        .map_err(|e| {
            tracing::error!(error = %e, "next key version failed");
            AppError::Internal
        })?;
        Ok(row.0.unwrap_or(0) + 1)
    }
}
