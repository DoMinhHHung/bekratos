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
    /// Gắn repository token và hàng đợi email với pool PostgreSQL; chưa thực hiện truy vấn.
    pub fn new(pool: &'a PgPool) -> Self {
        Self { pool }
    }

    /// Tạo và trả token có hạn dùng UTC, đồng thời đưa email vào hàng đợi thử gửi ngay.
    /// `purpose` nhận `email_verify`, `password_reset` hoặc `account_restore`.
    /// Vi phạm ràng buộc hoặc lỗi cơ sở dữ liệu trả `Internal`.
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

    /// Tạo và trả token cùng lịch gửi email ngay trong transaction.
    /// `purpose` nhận `email_verify`, `password_reset` hoặc `account_restore`;
    /// `expires_at` là thời điểm UTC. Lỗi cơ sở dữ liệu trả `Internal`.
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

    /// Thu hồi token chưa dùng, chưa thu hồi của người dùng theo mục đích trong transaction.
    /// Bao gồm token đã hết hạn; trả số hàng đổi, hoặc `Internal` nếu truy vấn thất bại.
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

    /// Đánh dấu đã dùng và trả token đúng ID, mục đích, chưa dùng, chưa thu hồi và còn hạn.
    /// Thao tác nguyên tử trong transaction; bên gọi quyết định commit hoặc rollback.
    /// Trả `None` nếu không đủ điều kiện, `Internal` nếu truy vấn thất bại.
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

    /// Nhận tối đa `limit` token chưa gửi, chưa dùng, chưa thu hồi, còn hạn và đến lịch thử gửi.
    /// Ưu tiên bản ghi cũ, bỏ qua hàng đang bị khóa; đặt quyền xử lý trong `lease_secs` giây
    /// và tăng số lần thử. Quyền xử lý phải hết hạn trước thời điểm hiện tại mới được nhận lại.
    /// Trả các bản ghi đã cập nhật hoặc `Internal` nếu truy vấn thất bại.
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

    /// Ghi thời điểm đã gửi, xóa quyền xử lý, lỗi gửi và lịch thử lại của token.
    /// ID không tồn tại vẫn thành công; lỗi truy vấn trả `Internal`.
    pub async fn mark_sent(&self, id: Uuid) -> AppResult<()> {
        sqlx::query(
            r#"
            UPDATE token.verification_tokens
            SET sent_at = now(),
                lease_until = NULL,
                last_delivery_error_code = NULL,
                next_attempt_at = NULL
            WHERE id = $1
            "#,
        )
        .bind(id)
        .execute(self.pool)
        .await
        .map_err(|e| {
            tracing::error!(error = %e, %id, "mark token sent failed");
            AppError::Internal
        })?;
        Ok(())
    }

    /// Lưu mã lỗi gửi, bỏ quyền xử lý và hẹn thử lại sau `backoff_secs` giây.
    /// Không tăng số lần thử tại đây; ID không tồn tại vẫn thành công.
    /// Lỗi truy vấn trả `Internal`.
    pub async fn mark_delivery_failure(
        &self,
        id: Uuid,
        error_code: &str,
        backoff_secs: i64,
    ) -> AppResult<()> {
        sqlx::query(
            r#"
            UPDATE token.verification_tokens
            SET lease_until = NULL,
                last_delivery_error_code = $2,
                next_attempt_at = now() + make_interval(secs => $3::double precision)
            WHERE id = $1
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
        Ok(())
    }
}
