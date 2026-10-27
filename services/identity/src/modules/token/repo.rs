use chrono::{DateTime, Utc};
use sqlx::{PgPool, Postgres, Transaction};
use uuid::Uuid;

use crate::error::{AppError, AppResult};

#[derive(Debug, Clone, sqlx::FromRow)]
pub struct VerificationTokenRow {
    pub id: Uuid,
    pub user_id: Uuid,
    pub purpose: String,
    pub email: String,
    pub expires_at: DateTime<Utc>,
    pub consumed_at: Option<DateTime<Utc>>,
    pub revoked_at: Option<DateTime<Utc>>,
    pub sent_at: Option<DateTime<Utc>>,
    pub delivery_attempts: i32,
    pub next_attempt_at: Option<DateTime<Utc>>,
    pub lease_until: Option<DateTime<Utc>>,
    pub last_delivery_error_code: Option<String>,
    pub created_at: DateTime<Utc>,
}

pub struct TokenRepo<'a> {
    pool: &'a PgPool,
}

impl<'a> TokenRepo<'a> {
    pub fn new(pool: &'a PgPool) -> Self {
        Self { pool }
    }

    /// Lưu token theo mục đích với địa chỉ email nhận và hạn dùng, sẵn sàng để worker gửi.
    ///
    /// Trả bản ghi mới hoặc `Internal` khi database lỗi.
    pub async fn insert(
        &self,
        id: Uuid,
        user_id: Uuid,
        purpose: &str,
        email: &str,
        expires_at: DateTime<Utc>,
    ) -> AppResult<VerificationTokenRow> {
        sqlx::query_as::<_, VerificationTokenRow>(
            r#"
            INSERT INTO token.verification_tokens (
                id, user_id, purpose, email, expires_at, next_attempt_at
            )
            VALUES ($1, $2, $3, $4, $5, now())
            RETURNING
                id, user_id, purpose, email, expires_at,
                consumed_at, revoked_at, sent_at, delivery_attempts,
                next_attempt_at, lease_until, last_delivery_error_code, created_at
            "#,
        )
        .bind(id)
        .bind(user_id)
        .bind(purpose)
        .bind(email)
        .bind(expires_at)
        .fetch_one(self.pool)
        .await
        .map_err(|e| {
            tracing::error!(error = %e, "insert verification token failed");
            AppError::Internal
        })
    }

    /// Lưu token sẵn sàng để worker gửi trong giao dịch của bên gọi.
    ///
    /// `purpose` phân biệt xác minh email, đặt lại mật khẩu và khôi phục tài khoản.
    /// Trả bản ghi mới hoặc `Internal` khi database lỗi.
    pub async fn insert_tx(
        tx: &mut Transaction<'_, Postgres>,
        id: Uuid,
        user_id: Uuid,
        purpose: &str,
        email: &str,
        expires_at: DateTime<Utc>,
    ) -> AppResult<VerificationTokenRow> {
        sqlx::query_as::<_, VerificationTokenRow>(
            r#"
            INSERT INTO token.verification_tokens (
                id, user_id, purpose, email, expires_at, next_attempt_at
            )
            VALUES ($1, $2, $3, $4, $5, now())
            RETURNING
                id, user_id, purpose, email, expires_at,
                consumed_at, revoked_at, sent_at, delivery_attempts,
                next_attempt_at, lease_until, last_delivery_error_code, created_at
            "#,
        )
        .bind(id)
        .bind(user_id)
        .bind(purpose)
        .bind(email)
        .bind(expires_at)
        .fetch_one(&mut **tx)
        .await
        .map_err(|e| {
            tracing::error!(error = %e, "insert verification token tx failed");
            AppError::Internal
        })
    }

    /// Thu hồi token cùng người dùng và mục đích chưa dùng/chưa thu hồi, kể cả token hết hạn,
    /// trong giao dịch của bên gọi. Trả số hàng cập nhật hoặc `Internal` khi truy vấn lỗi.
    pub async fn revoke_active_for_user_purpose_tx(
        tx: &mut Transaction<'_, Postgres>,
        user_id: Uuid,
        purpose: &str,
    ) -> AppResult<u64> {
        let result = sqlx::query(
            r#"
            UPDATE token.verification_tokens
            SET revoked_at = now()
            WHERE user_id = $1
              AND purpose = $2
              AND consumed_at IS NULL
              AND revoked_at IS NULL
            "#,
        )
        .bind(user_id)
        .bind(purpose)
        .execute(&mut **tx)
        .await
        .map_err(|e| {
            tracing::error!(error = %e, %user_id, "revoke active tokens failed");
            AppError::Internal
        })?;
        Ok(result.rows_affected())
    }

    /// Đánh dấu nguyên tử token đúng mục đích, chưa dùng/thu hồi và chưa hết hạn là đã dùng.
    ///
    /// Trả bản ghi trong giao dịch của bên gọi, `None` nếu không đủ điều kiện
    /// hoặc `Internal` khi truy vấn lỗi; token đến đúng hạn hết hiệu lực không được dùng.
    pub async fn consume_tx(
        tx: &mut Transaction<'_, Postgres>,
        id: Uuid,
        purpose: &str,
    ) -> AppResult<Option<VerificationTokenRow>> {
        let row = sqlx::query_as::<_, VerificationTokenRow>(
            r#"
            UPDATE token.verification_tokens
            SET consumed_at = now()
            WHERE id = $1
              AND purpose = $2
              AND consumed_at IS NULL
              AND revoked_at IS NULL
              AND expires_at > now()
            RETURNING
                id, user_id, purpose, email, expires_at,
                consumed_at, revoked_at, sent_at, delivery_attempts,
                next_attempt_at, lease_until, last_delivery_error_code, created_at
            "#,
        )
        .bind(id)
        .bind(purpose)
        .fetch_optional(&mut **tx)
        .await
        .map_err(|e| {
            tracing::error!(error = %e, %id, "consume verification token failed");
            AppError::Internal
        })?;
        Ok(row)
    }

    /// Nhận tối đa `limit` token chưa gửi, chưa dùng/thu hồi, còn hạn và đã đến lịch gửi.
    ///
    /// Ưu tiên token tạo trước, bỏ qua hàng bị khóa; đặt lease dài `lease_secs` giây
    /// và tăng số lần thử trước khi trả bản ghi. Lease cũ phải hết hạn trước thời điểm
    /// hiện tại mới được nhận lại. Lỗi database trả `Internal`.
    pub async fn claim_due_for_delivery(
        &self,
        limit: i64,
        lease_secs: i64,
    ) -> AppResult<Vec<VerificationTokenRow>> {
        sqlx::query_as::<_, VerificationTokenRow>(
            r#"
            WITH due AS (
                SELECT id
                FROM token.verification_tokens
                WHERE consumed_at IS NULL
                  AND revoked_at IS NULL
                  AND expires_at > now()
                  AND sent_at IS NULL
                  AND (lease_until IS NULL OR lease_until < now())
                  AND (next_attempt_at IS NULL OR next_attempt_at <= now())
                ORDER BY created_at ASC
                LIMIT $1
                FOR UPDATE SKIP LOCKED
            )
            UPDATE token.verification_tokens t
            SET
                lease_until = now() + make_interval(secs => $2::double precision),
                delivery_attempts = t.delivery_attempts + 1
            FROM due
            WHERE t.id = due.id
            RETURNING
                t.id, t.user_id, t.purpose, t.email, t.expires_at,
                t.consumed_at, t.revoked_at, t.sent_at, t.delivery_attempts,
                t.next_attempt_at, t.lease_until, t.last_delivery_error_code, t.created_at
            "#,
        )
        .bind(limit)
        .bind(lease_secs)
        .fetch_all(self.pool)
        .await
        .map_err(|e| {
            tracing::error!(error = %e, "claim due tokens failed");
            AppError::Internal
        })
    }

    pub async fn mark_delivery_permanent_failure(
        &self,
        id: Uuid,
        error_code: &str,
    ) -> AppResult<()> {
        let result = sqlx::query(
            r#"
            UPDATE token.verification_tokens
            SET lease_until = NULL,
                last_delivery_error_code = $2,
                next_attempt_at = expires_at
            WHERE id = $1
              AND sent_at IS NULL
            "#,
        )
        .bind(id)
        .bind(error_code)
        .execute(self.pool)
        .await
        .map_err(|e| {
            tracing::error!(%id, "mark delivery permanently failed");
            AppError::Internal
        })?;
        if result.rows_affected() != 1 {
            tracing::error!(%id, "mark permanent delivery failure affected unexpected row count");
            return Err(AppError::Internal);
        }
        Ok(())
    }

    pub async fn mark_sent(&self, id: Uuid) -> AppResult<()> {
        let result = sqlx::query(
            r#"
            UPDATE token.verification_tokens
            SET sent_at = now(),
                lease_until = NULL,
                last_delivery_error_code = NULL,
                next_attempt_at = NULL
            WHERE id = $1
              AND sent_at IS NULL
            "#,
        )
        .bind(id)
        .execute(self.pool)
        .await
        .map_err(|e| {
            tracing::error!(error = %e, %id, "mark token sent failed");
            AppError::Internal
        })?;
        if result.rows_affected() != 1 {
            tracing::error!(%id, "mark token sent affected unexpected row count");
            return Err(AppError::Internal);
        }
        Ok(())
    }

    pub async fn mark_delivery_failure(
        &self,
        id: Uuid,
        error_code: &str,
        backoff_secs: i64,
    ) -> AppResult<()> {
        let result = sqlx::query(
            r#"
            UPDATE token.verification_tokens
            SET lease_until = NULL,
                last_delivery_error_code = $2,
                next_attempt_at = now() + make_interval(secs => $3::double precision)
            WHERE id = $1
              AND sent_at IS NULL
              AND consumed_at IS NULL
              AND revoked_at IS NULL
              AND expires_at > now()
            "#,
        )
        .bind(id)
        .bind(error_code)
        .bind(backoff_secs)
        .execute(self.pool)
        .await
        .map_err(|e| {
            tracing::error!(error = %e, %id, "mark delivery failure failed");
            AppError::Internal
        })?;
        if result.rows_affected() != 1 {
            tracing::error!(%id, "mark delivery failure affected unexpected row count");
            return Err(AppError::Internal);
        }
        Ok(())
    }
}
