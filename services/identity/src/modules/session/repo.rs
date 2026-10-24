use chrono::{DateTime, Utc};
use sqlx::{PgPool, Postgres, Transaction};
use uuid::Uuid;

use crate::error::{AppError, AppResult};

#[derive(Debug, Clone, sqlx::FromRow)]
pub struct SessionRow {
    pub id: Uuid,
    pub user_id: Uuid,
    pub ip: Option<String>,
    pub user_agent: Option<String>,
    pub device_name: Option<String>,
    pub last_seen_at: DateTime<Utc>,
    pub expires_at: DateTime<Utc>,
    pub revoked_at: Option<DateTime<Utc>>,
    pub revoked_reason: Option<String>,
    pub created_at: DateTime<Utc>,
}

#[derive(Debug, Clone, sqlx::FromRow)]
pub struct RefreshTokenRow {
    pub id: Uuid,
    pub session_id: Uuid,
    pub parent_id: Option<Uuid>,
    pub token_hash: String,
    pub expires_at: DateTime<Utc>,
    pub used_at: Option<DateTime<Utc>>,
    pub revoked_at: Option<DateTime<Utc>>,
    pub created_at: DateTime<Utc>,
}

pub struct SessionRepo<'a> {
    pool: &'a PgPool,
}

impl<'a> SessionRepo<'a> {
    /// Gắn repository phiên với pool PostgreSQL được cấp; chưa thực hiện truy vấn.
    pub fn new(pool: &'a PgPool) -> Self {
        Self { pool }
    }

    /// Tạo và trả bản ghi phiên trong transaction với thời điểm hết hạn UTC được cấp.
    /// Không tạo refresh token; lỗi cơ sở dữ liệu trả `Internal`.
    pub async fn insert_tx(
        tx: &mut Transaction<'_, Postgres>,
        id: Uuid,
        user_id: Uuid,
        ip: Option<&str>,
        user_agent: Option<&str>,
        device_name: Option<&str>,
        expires_at: DateTime<Utc>,
    ) -> AppResult<SessionRow> {
        sqlx::query_as::<_, SessionRow>(
            r#"
            INSERT INTO session.sessions (
                id, user_id, ip, user_agent, device_name, expires_at
            )
            VALUES ($1, $2, $3, $4, $5, $6)
            RETURNING
                id, user_id, ip, user_agent, device_name,
                last_seen_at, expires_at, revoked_at, revoked_reason, created_at
            "#,
        )
        .bind(id)
        .bind(user_id)
        .bind(ip)
        .bind(user_agent)
        .bind(device_name)
        .bind(expires_at)
        .fetch_one(&mut **tx)
        .await
        .map_err(|e| {
            tracing::error!(error = %e, %user_id, "insert session failed");
            AppError::Internal
        })
    }

    /// Lấy phiên theo ID, kể cả đã hết hạn hoặc bị thu hồi; không có thì trả `None`.
    /// Lỗi truy vấn trả `Internal`.
    pub async fn find_by_id(&self, id: Uuid) -> AppResult<Option<SessionRow>> {
        sqlx::query_as::<_, SessionRow>(
            r#"
            SELECT
                id, user_id, ip, user_agent, device_name,
                last_seen_at, expires_at, revoked_at, revoked_reason, created_at
            FROM session.sessions
            WHERE id = $1
            "#,
        )
        .bind(id)
        .fetch_optional(self.pool)
        .await
        .map_err(|e| {
            tracing::error!(error = %e, %id, "find session failed");
            AppError::Internal
        })
    }

    /// Liệt kê phiên chưa thu hồi và có hạn dùng lớn hơn thời điểm hiện tại của PostgreSQL,
    /// theo lần hoạt động gần nhất trước; lỗi truy vấn trả `Internal`.
    pub async fn list_active_for_user(&self, user_id: Uuid) -> AppResult<Vec<SessionRow>> {
        sqlx::query_as::<_, SessionRow>(
            r#"
            SELECT
                id, user_id, ip, user_agent, device_name,
                last_seen_at, expires_at, revoked_at, revoked_reason, created_at
            FROM session.sessions
            WHERE user_id = $1
              AND revoked_at IS NULL
              AND expires_at > now()
            ORDER BY last_seen_at DESC
            "#,
        )
        .bind(user_id)
        .fetch_all(self.pool)
        .await
        .map_err(|e| {
            tracing::error!(error = %e, %user_id, "list active sessions for user failed");
            AppError::Internal
        })
    }

    /// Lấy và khóa hàng phiên đến khi transaction kết thúc, không lọc trạng thái hay hạn dùng.
    /// Trả `None` nếu không có phiên, `Internal` nếu truy vấn thất bại.
    pub async fn find_by_id_for_update_tx(
        tx: &mut Transaction<'_, Postgres>,
        id: Uuid,
    ) -> AppResult<Option<SessionRow>> {
        sqlx::query_as::<_, SessionRow>(
            r#"
            SELECT
                id, user_id, ip, user_agent, device_name,
                last_seen_at, expires_at, revoked_at, revoked_reason, created_at
            FROM session.sessions
            WHERE id = $1
            FOR UPDATE
            "#,
        )
        .bind(id)
        .fetch_optional(&mut **tx)
        .await
        .map_err(|e| {
            tracing::error!(error = %e, %id, "lock session failed");
            AppError::Internal
        })
    }

    /// Thu hồi phiên chưa bị thu hồi với lý do được cấp trong transaction, trả số hàng đổi.
    /// Không có phiên hoặc đã thu hồi thì trả 0; lỗi truy vấn trả `Internal`.
    /// Hàm không thu hồi refresh token của phiên.
    pub async fn revoke_tx(
        tx: &mut Transaction<'_, Postgres>,
        id: Uuid,
        reason: &str,
    ) -> AppResult<u64> {
        let res = sqlx::query(
            r#"
            UPDATE session.sessions
            SET revoked_at = now(), revoked_reason = $2
            WHERE id = $1 AND revoked_at IS NULL
            "#,
        )
        .bind(id)
        .bind(reason)
        .execute(&mut **tx)
        .await
        .map_err(|e| {
            tracing::error!(error = %e, %id, "revoke session failed");
            AppError::Internal
        })?;
        Ok(res.rows_affected())
    }

    /// Cập nhật lần hoạt động gần nhất của phiên chưa thu hồi trong transaction.
    /// Không có hàng phù hợp vẫn thành công; lỗi truy vấn trả `Internal`.
    pub async fn touch_last_seen_tx(tx: &mut Transaction<'_, Postgres>, id: Uuid) -> AppResult<()> {
        sqlx::query(
            r#"
            UPDATE session.sessions
            SET last_seen_at = now()
            WHERE id = $1 AND revoked_at IS NULL
            "#,
        )
        .bind(id)
        .execute(&mut **tx)
        .await
        .map_err(|e| {
            tracing::error!(error = %e, %id, "touch session failed");
            AppError::Internal
        })?;
        Ok(())
    }

    /// Thu hồi các phiên chưa thu hồi của người dùng trong transaction, trả số hàng đổi.
    /// `except_session_id` giữ lại phiên được chỉ định; `None` không giữ lại phiên nào.
    /// Không thu hồi refresh token; lỗi truy vấn trả `Internal`.
    pub async fn revoke_all_for_user_tx(
        tx: &mut Transaction<'_, Postgres>,
        user_id: Uuid,
        reason: &str,
        except_session_id: Option<Uuid>,
    ) -> AppResult<u64> {
        let res = if let Some(except) = except_session_id {
            sqlx::query(
                r#"
                UPDATE session.sessions
                SET revoked_at = now(), revoked_reason = $2
                WHERE user_id = $1
                  AND revoked_at IS NULL
                  AND id <> $3
                "#,
            )
            .bind(user_id)
            .bind(reason)
            .bind(except)
            .execute(&mut **tx)
            .await
        } else {
            sqlx::query(
                r#"
                UPDATE session.sessions
                SET revoked_at = now(), revoked_reason = $2
                WHERE user_id = $1 AND revoked_at IS NULL
                "#,
            )
            .bind(user_id)
            .bind(reason)
            .execute(&mut **tx)
            .await
        }
        .map_err(|e| {
            tracing::error!(error = %e, %user_id, "revoke all sessions for user failed");
            AppError::Internal
        })?;
        Ok(res.rows_affected())
    }
}

pub struct RefreshTokenRepo;

impl RefreshTokenRepo {
    /// Lưu và trả bản ghi refresh token trong transaction; `token_hash` là hash đã tính sẵn.
    /// `parent_id` chỉ token được thay thế, hoặc `None` cho token đầu tiên của phiên.
    /// Lỗi cơ sở dữ liệu, kể cả vi phạm tính duy nhất, trả `Internal`.
    pub async fn insert_tx(
        tx: &mut Transaction<'_, Postgres>,
        id: Uuid,
        session_id: Uuid,
        parent_id: Option<Uuid>,
        token_hash: &str,
        expires_at: DateTime<Utc>,
    ) -> AppResult<RefreshTokenRow> {
        sqlx::query_as::<_, RefreshTokenRow>(
            r#"
            INSERT INTO session.refresh_tokens (
                id, session_id, parent_id, token_hash, expires_at
            )
            VALUES ($1, $2, $3, $4, $5)
            RETURNING
                id, session_id, parent_id, token_hash, expires_at,
                used_at, revoked_at, created_at
            "#,
        )
        .bind(id)
        .bind(session_id)
        .bind(parent_id)
        .bind(token_hash)
        .bind(expires_at)
        .fetch_one(&mut **tx)
        .await
        .map_err(|e| {
            tracing::error!(error = %e, "insert refresh token failed");
            AppError::Internal
        })
    }

    /// Lấy và khóa refresh token theo hash đến khi transaction kết thúc, kể cả token đã dùng,
    /// đã thu hồi hay hết hạn. Không có thì trả `None`; lỗi truy vấn trả `Internal`.
    pub async fn find_by_hash_for_update_tx(
        tx: &mut Transaction<'_, Postgres>,
        token_hash: &str,
    ) -> AppResult<Option<RefreshTokenRow>> {
        sqlx::query_as::<_, RefreshTokenRow>(
            r#"
            SELECT
                id, session_id, parent_id, token_hash, expires_at,
                used_at, revoked_at, created_at
            FROM session.refresh_tokens
            WHERE token_hash = $1
            FOR UPDATE
            "#,
        )
        .bind(token_hash)
        .fetch_optional(&mut **tx)
        .await
        .map_err(|e| {
            tracing::error!(error = %e, "find refresh token failed");
            AppError::Internal
        })
    }

    /// Đánh dấu refresh token chưa dùng và chưa thu hồi là đã dùng trong transaction.
    /// Không có hàng phù hợp vẫn thành công; lỗi truy vấn trả `Internal`.
    pub async fn mark_used_tx(tx: &mut Transaction<'_, Postgres>, id: Uuid) -> AppResult<()> {
        sqlx::query(
            r#"
            UPDATE session.refresh_tokens
            SET used_at = now()
            WHERE id = $1 AND used_at IS NULL AND revoked_at IS NULL
            "#,
        )
        .bind(id)
        .execute(&mut **tx)
        .await
        .map_err(|e| {
            tracing::error!(error = %e, %id, "mark refresh used failed");
            AppError::Internal
        })?;
        Ok(())
    }

    /// Thu hồi mọi refresh token chưa thu hồi của phiên trong transaction, trả số hàng đổi.
    /// Bao gồm token đã dùng hoặc hết hạn; lỗi truy vấn trả `Internal`.
    pub async fn revoke_all_for_session_tx(
        tx: &mut Transaction<'_, Postgres>,
        session_id: Uuid,
    ) -> AppResult<u64> {
        let res = sqlx::query(
            r#"
            UPDATE session.refresh_tokens
            SET revoked_at = now()
            WHERE session_id = $1 AND revoked_at IS NULL
            "#,
        )
        .bind(session_id)
        .execute(&mut **tx)
        .await
        .map_err(|e| {
            tracing::error!(error = %e, %session_id, "revoke session refresh tokens failed");
            AppError::Internal
        })?;
        Ok(res.rows_affected())
    }

    /// Thu hồi refresh token chưa thu hồi thuộc các phiên của người dùng, trả số hàng đổi.
    /// Nếu có `except_session_id`, giữ nguyên token của phiên đó; `None` thu hồi tất cả.
    /// Lỗi truy vấn trả `Internal`.
    pub async fn revoke_all_for_user_tx(
        tx: &mut Transaction<'_, Postgres>,
        user_id: Uuid,
        except_session_id: Option<Uuid>,
    ) -> AppResult<u64> {
        let res = if let Some(except) = except_session_id {
            sqlx::query(
                r#"
                UPDATE session.refresh_tokens rt
                SET revoked_at = now()
                FROM session.sessions s
                WHERE rt.session_id = s.id
                  AND s.user_id = $1
                  AND rt.revoked_at IS NULL
                  AND s.id <> $2
                "#,
            )
            .bind(user_id)
            .bind(except)
            .execute(&mut **tx)
            .await
        } else {
            sqlx::query(
                r#"
                UPDATE session.refresh_tokens rt
                SET revoked_at = now()
                FROM session.sessions s
                WHERE rt.session_id = s.id
                  AND s.user_id = $1
                  AND rt.revoked_at IS NULL
                "#,
            )
            .bind(user_id)
            .execute(&mut **tx)
            .await
        }
        .map_err(|e| {
            tracing::error!(error = %e, %user_id, "revoke all refresh tokens for user failed");
            AppError::Internal
        })?;
        Ok(res.rows_affected())
    }
}
