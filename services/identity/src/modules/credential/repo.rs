use sqlx::{PgPool, Postgres, Transaction};
use uuid::Uuid;

use crate::error::{AppError, AppResult};

pub struct CredentialRepo;

impl CredentialRepo {
    /// Lưu hash mật khẩu đã được tạo sẵn trong transaction; lỗi cơ sở dữ liệu trả `Internal`.
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

    /// Thêm hash vào lịch sử mật khẩu trong transaction, chưa cắt bớt lịch sử.
    /// Trả `Internal` nếu ghi cơ sở dữ liệu thất bại.
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

    /// Lấy hash mật khẩu hiện tại, hoặc `None` nếu chưa có credential.
    /// Trả `Internal` khi truy vấn thất bại.
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

    /// Lấy hash hiện tại trong transaction, hoặc `None` nếu chưa có credential.
    /// Truy vấn không khóa hàng; lỗi cơ sở dữ liệu trả `Internal`.
    pub async fn find_password_hash_tx(
        tx: &mut Transaction<'_, Postgres>,
        user_id: Uuid,
    ) -> AppResult<Option<String>> {
        let row: Option<(String,)> = sqlx::query_as(
            r#"
            SELECT password_hash
            FROM credential.password_credentials
            WHERE user_id = $1
            "#,
        )
        .bind(user_id)
        .fetch_optional(&mut **tx)
        .await
        .map_err(|e| {
            tracing::error!(error = %e, %user_id, "find password hash tx failed");
            AppError::Internal
        })?;
        Ok(row.map(|r| r.0))
    }

    /// Thay hash và cập nhật thời điểm đổi mật khẩu trong transaction.
    /// Trả `Internal` nếu truy vấn thất bại hoặc không cập nhật đúng một credential.
    pub async fn update_password_tx(
        tx: &mut Transaction<'_, Postgres>,
        user_id: Uuid,
        password_hash: &str,
    ) -> AppResult<()> {
        let res = sqlx::query(
            r#"
            UPDATE credential.password_credentials
            SET password_hash = $2,
                password_changed_at = now(),
                updated_at = now()
            WHERE user_id = $1
            "#,
        )
        .bind(user_id)
        .bind(password_hash)
        .execute(&mut **tx)
        .await
        .map_err(|e| {
            tracing::error!(error = %e, %user_id, "update password failed");
            AppError::Internal
        })?;
        if res.rows_affected() != 1 {
            return Err(AppError::Internal);
        }
        Ok(())
    }

    /// Lấy tối đa `limit` hash từ lịch sử, theo thời điểm tạo mới nhất trước.
    /// Giới hạn âm bị PostgreSQL từ chối; lỗi truy vấn trả `Internal`.
    pub async fn list_recent_hashes(
        pool: &PgPool,
        user_id: Uuid,
        limit: i64,
    ) -> AppResult<Vec<String>> {
        let rows: Vec<(String,)> = sqlx::query_as(
            r#"
            SELECT password_hash
            FROM credential.password_history
            WHERE user_id = $1
            ORDER BY created_at DESC
            LIMIT $2
            "#,
        )
        .bind(user_id)
        .bind(limit)
        .fetch_all(pool)
        .await
        .map_err(|e| {
            tracing::error!(error = %e, %user_id, "list password history failed");
            AppError::Internal
        })?;
        Ok(rows.into_iter().map(|r| r.0).collect())
    }

    /// Lấy tối đa `limit` hash mới nhất từ lịch sử trong transaction.
    /// Giới hạn âm bị PostgreSQL từ chối; lỗi truy vấn trả `Internal`.
    pub async fn list_recent_hashes_tx(
        tx: &mut Transaction<'_, Postgres>,
        user_id: Uuid,
        limit: i64,
    ) -> AppResult<Vec<String>> {
        let rows: Vec<(String,)> = sqlx::query_as(
            r#"
            SELECT password_hash
            FROM credential.password_history
            WHERE user_id = $1
            ORDER BY created_at DESC
            LIMIT $2
            "#,
        )
        .bind(user_id)
        .bind(limit)
        .fetch_all(&mut **tx)
        .await
        .map_err(|e| {
            tracing::error!(error = %e, %user_id, "list password history failed");
            AppError::Internal
        })?;
        Ok(rows.into_iter().map(|r| r.0).collect())
    }

    /// Xóa lịch sử cũ, giữ tối đa `keep` hash mới nhất trong transaction; 0 xóa toàn bộ.
    /// Giới hạn âm hoặc lỗi truy vấn trả `Internal`.
    pub async fn trim_history_tx(
        tx: &mut Transaction<'_, Postgres>,
        user_id: Uuid,
        keep: i64,
    ) -> AppResult<()> {
        sqlx::query(
            r#"
            DELETE FROM credential.password_history
            WHERE user_id = $1
              AND id NOT IN (
                  SELECT id
                  FROM credential.password_history
                  WHERE user_id = $1
                  ORDER BY created_at DESC
                  LIMIT $2
              )
            "#,
        )
        .bind(user_id)
        .bind(keep)
        .execute(&mut **tx)
        .await
        .map_err(|e| {
            tracing::error!(error = %e, %user_id, "trim password history failed");
            AppError::Internal
        })?;
        Ok(())
    }
}
