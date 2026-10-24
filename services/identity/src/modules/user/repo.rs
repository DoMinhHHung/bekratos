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
    /// Gắn repository người dùng với pool PostgreSQL được cấp; chưa thực hiện truy vấn.
    pub fn new(pool: &'a PgPool) -> Self {
        Self { pool }
    }

    /// Lấy người dùng ở mọi trạng thái theo ID; không có thì trả `None`.
    /// Lỗi truy vấn trả `Internal`.
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

    /// Lấy người dùng cùng bản ghi email không phân biệt hoa thường, không yêu cầu đã xác minh.
    /// Trả `None` nếu thiếu email hoặc người dùng; lỗi truy vấn trả `Internal`.
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

    /// Lấy người dùng theo username không phân biệt hoa thường, không lọc trạng thái tài khoản.
    /// Trả `None` nếu không có, `Internal` nếu truy vấn thất bại.
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

    /// Tạo và trả tài khoản pending với locale và múi giờ được cấp, chưa tạo hồ sơ hay credential.
    /// Mọi lỗi cơ sở dữ liệu, kể cả trùng ID, trả `Internal`.
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

    /// Tạo và trả tài khoản pending trong transaction, chưa tạo các bản ghi liên quan.
    /// Vi phạm tính duy nhất trả `Conflict`; lỗi cơ sở dữ liệu khác trả `Internal`.
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

    /// Thêm hồ sơ với tên đầy đủ và tên hiển thị trong transaction.
    /// Lỗi cơ sở dữ liệu, kể cả hồ sơ đã tồn tại, trả `Internal`.
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

    /// Thêm email chưa xác minh trong transaction; `is_primary` đánh dấu email chính.
    /// Vi phạm tính duy nhất của ID, email hoặc email chính trả `Conflict`;
    /// lỗi cơ sở dữ liệu khác trả `Internal`.
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

    /// Thêm username trong transaction; PostgreSQL kiểm tra định dạng và tính duy nhất.
    /// Vi phạm tính duy nhất trả `Conflict`; lỗi khác, kể cả sai định dạng, trả `Internal`.
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

    /// Ghi nhận chấp nhận chính sách cùng phiên bản, hash tài liệu và ngữ cảnh yêu cầu
    /// trong transaction; lỗi cơ sở dữ liệu trả `Internal`.
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

    /// Chuyển người dùng pending sang active và đánh dấu email chưa xác minh là đã xác minh.
    /// Trả `BadRequest` nếu một trong hai cập nhật không đổi đúng một hàng,
    /// `Internal` nếu truy vấn thất bại; bên gọi cần rollback transaction khi lỗi.
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

/// Nhận diện lỗi vi phạm tính duy nhất của PostgreSQL bằng SQLSTATE `23505`.
pub fn is_unique_violation(err: &sqlx::Error) -> bool {
    match err {
        sqlx::Error::Database(db) => db.code().as_deref() == Some("23505"),
        _ => false,
    }
}

/// Chuyển vi phạm tính duy nhất thành `Conflict`, mọi lỗi SQLx khác thành `Internal`.
fn map_db_err(err: sqlx::Error, context: &str) -> AppError {
    if is_unique_violation(&err) {
        tracing::debug!(error = %err, "{context} unique violation");
        return AppError::Conflict;
    }
    tracing::error!(error = %err, "{context}");
    AppError::Internal
}

impl<'a> UserRepo<'a> {
    /// Lấy người dùng kèm hồ sơ, email chính và username; thành phần liên quan thiếu là `None`.
    /// Không có người dùng thì trả `None`; lỗi truy vấn trả `Internal`.
    pub async fn load_profile_bundle(&self, user_id: Uuid) -> AppResult<Option<ProfileBundle>> {
        #[derive(sqlx::FromRow)]
        struct Bundle {
            id: Uuid,
            status: String,
            status_before_deletion: Option<String>,
            locale: Option<String>,
            timezone: Option<String>,
            deletion_requested_at: Option<chrono::DateTime<chrono::Utc>>,
            purge_after: Option<chrono::DateTime<chrono::Utc>>,
            created_at: chrono::DateTime<chrono::Utc>,
            updated_at: chrono::DateTime<chrono::Utc>,
            full_name: Option<String>,
            display_name: Option<String>,
            avatar_url: Option<String>,
            email: Option<String>,
            username: Option<String>,
        }
        let row: Option<Bundle> = sqlx::query_as(
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
                u.updated_at,
                p.full_name,
                p.display_name,
                p.avatar_url,
                e.email,
                un.username
            FROM "user".users u
            LEFT JOIN "user".user_profile p ON p.user_id = u.id
            LEFT JOIN "user".user_emails e ON e.user_id = u.id AND e.is_primary = true
            LEFT JOIN "user".user_usernames un ON un.user_id = u.id
            WHERE u.id = $1
            "#,
        )
        .bind(user_id)
        .fetch_optional(self.pool)
        .await
        .map_err(|e| {
            tracing::error!(error = %e, %user_id, "load profile bundle failed");
            AppError::Internal
        })?;
        Ok(row.map(|b| ProfileBundle {
            user: UserRow {
                id: b.id,
                status: b.status,
                status_before_deletion: b.status_before_deletion,
                locale: b.locale,
                timezone: b.timezone,
                deletion_requested_at: b.deletion_requested_at,
                purge_after: b.purge_after,
                created_at: b.created_at,
                updated_at: b.updated_at,
            },
            full_name: b.full_name,
            display_name: b.display_name,
            avatar_url: b.avatar_url,
            email: b.email,
            username: b.username,
        }))
    }

    /// Lấy bộ giá trị `(status, full_name, display_name, avatar_url)` của người dùng.
    /// Thiếu hồ sơ vẫn trả status với các tên và ảnh là `None`; thiếu người dùng trả `None`.
    /// Lỗi truy vấn trả `Internal`.
    pub async fn find_profile(&self, user_id: Uuid) -> AppResult<Option<ProfileRow>> {
        let row: Option<ProfileRow> = sqlx::query_as(
            r#"
            SELECT
                u.status,
                p.full_name,
                p.display_name,
                p.avatar_url
            FROM "user".users u
            LEFT JOIN "user".user_profile p ON p.user_id = u.id
            WHERE u.id = $1
            "#,
        )
        .bind(user_id)
        .fetch_optional(self.pool)
        .await
        .map_err(|e| {
            tracing::error!(error = %e, %user_id, "find profile failed");
            AppError::Internal
        })?;
        Ok(row)
    }

    /// Lấy email chính mà không yêu cầu đã xác minh; không có thì trả `None`.
    /// Lỗi truy vấn trả `Internal`.
    pub async fn find_primary_email(&self, user_id: Uuid) -> AppResult<Option<String>> {
        let row: Option<(String,)> = sqlx::query_as(
            r#"
            SELECT email FROM "user".user_emails
            WHERE user_id = $1 AND is_primary = true
            LIMIT 1
            "#,
        )
        .bind(user_id)
        .fetch_optional(self.pool)
        .await
        .map_err(|e| {
            tracing::error!(error = %e, %user_id, "find primary email failed");
            AppError::Internal
        })?;
        Ok(row.map(|r| r.0))
    }

    /// Lấy username của người dùng, hoặc `None` nếu chưa có; lỗi truy vấn trả `Internal`.
    pub async fn find_username(&self, user_id: Uuid) -> AppResult<Option<String>> {
        let row: Option<(String,)> = sqlx::query_as(
            r#"
            SELECT username FROM "user".user_usernames WHERE user_id = $1 LIMIT 1
            "#,
        )
        .bind(user_id)
        .fetch_optional(self.pool)
        .await
        .map_err(|e| {
            tracing::error!(error = %e, %user_id, "find username failed");
            AppError::Internal
        })?;
        Ok(row.map(|r| r.0))
    }

    /// Cập nhật các trường hồ sơ có giá trị trong transaction; `None` giữ nguyên trường cũ.
    /// Không có hồ sơ vẫn thành công; lỗi truy vấn trả `Internal`.
    pub async fn update_profile_tx(
        tx: &mut Transaction<'_, Postgres>,
        user_id: Uuid,
        full_name: Option<&str>,
        display_name: Option<&str>,
        avatar_url: Option<&str>,
    ) -> AppResult<()> {
        sqlx::query(
            r#"
            UPDATE "user".user_profile
            SET
                full_name = COALESCE($2, full_name),
                display_name = COALESCE($3, display_name),
                avatar_url = COALESCE($4, avatar_url),
                updated_at = now()
            WHERE user_id = $1
            "#,
        )
        .bind(user_id)
        .bind(full_name)
        .bind(display_name)
        .bind(avatar_url)
        .execute(&mut **tx)
        .await
        .map_err(|e| {
            tracing::error!(error = %e, %user_id, "update profile failed");
            AppError::Internal
        })?;
        Ok(())
    }
    /// Cập nhật locale và múi giờ trong transaction; `None` giữ nguyên giá trị cũ.
    /// Không có người dùng vẫn thành công; lỗi truy vấn trả `Internal`.
    pub async fn update_user_meta_tx(
        tx: &mut Transaction<'_, Postgres>,
        user_id: Uuid,
        locale: Option<&str>,
        timezone: Option<&str>,
    ) -> AppResult<()> {
        sqlx::query(
            r#"
                UPDATE "user".users
                SET
                    locale = COALESCE($2, locale),
                    timezone = COALESCE($3, timezone),
                    updated_at = now()
                WHERE id = $1
                "#,
        )
        .bind(user_id)
        .bind(locale)
        .bind(timezone)
        .execute(&mut **tx)
        .await
        .map_err(|e| {
            tracing::error!(error = %e, %user_id, "update user meta failed");
            AppError::Internal
        })?;
        Ok(())
    }
    /// Chuyển tài khoản active, suspended hoặc pending sang deleted, giữ trạng thái trước đó
    /// và đặt hạn purge UTC trong transaction. Trả `Conflict` nếu không đổi đúng một hàng,
    /// `Internal` nếu truy vấn thất bại; không thu hồi phiên tại đây.
    pub async fn soft_delete_tx(
        tx: &mut Transaction<'_, Postgres>,
        user_id: Uuid,
        purge_after: chrono::DateTime<chrono::Utc>,
    ) -> AppResult<()> {
        let res = sqlx::query(
            r#"
                UPDATE "user".users
                SET
                    status_before_deletion = status,
                    status = 'deleted',
                    deletion_requested_at = now(),
                    purge_after = $2,
                    updated_at = now()
                WHERE id = $1 AND status IN ('active', 'suspended', 'pending')
                "#,
        )
        .bind(user_id)
        .bind(purge_after)
        .execute(&mut **tx)
        .await
        .map_err(|e| {
            tracing::error!(error = %e, %user_id, "soft delete failed");
            AppError::Internal
        })?;
        if res.rows_affected() != 1 {
            return Err(AppError::Conflict);
        }
        Ok(())
    }

    /// Khôi phục tài khoản deleted về trạng thái trước khi xóa, mặc định active nếu thiếu.
    /// Chỉ khôi phục khi `purge_after` chưa tới hoặc không được đặt, rồi xóa các mốc xóa.
    /// Trả `BadRequest` nếu không đổi đúng một hàng, `Internal` nếu truy vấn thất bại.
    pub async fn restore_tx(tx: &mut Transaction<'_, Postgres>, user_id: Uuid) -> AppResult<()> {
        let res = sqlx::query(
            r#"
                UPDATE "user".users
                SET
                    status = COALESCE(status_before_deletion, 'active'),
                    status_before_deletion = NULL,
                    deletion_requested_at = NULL,
                    purge_after = NULL,
                    updated_at = now()
                WHERE id = $1
                  AND status = 'deleted'
                  AND (purge_after IS NULL OR purge_after > now())
                "#,
        )
        .bind(user_id)
        .execute(&mut **tx)
        .await
        .map_err(|e| {
            tracing::error!(error = %e, %user_id, "restore user failed");
            AppError::Internal
        })?;
        if res.rows_affected() != 1 {
            return Err(AppError::BadRequest("cannot restore".into()));
        }
        Ok(())
    }

    /// Xóa vĩnh viễn tối đa `limit` tài khoản deleted đã tới hạn purge, ưu tiên hạn cũ nhất
    /// và bỏ qua hàng đang bị khóa. Các bản ghi có khóa ngoại xóa dây chuyền cũng bị xóa.
    /// Trả số tài khoản đã xóa; `limit <= 0` trả 0, lỗi truy vấn trả `Internal`.
    pub async fn hard_delete_expired(pool: &sqlx::PgPool, limit: i64) -> AppResult<u64> {
        if limit <= 0 {
            return Ok(0);
        }

        let res = sqlx::query(
            r#"
                WITH expired AS (
                    SELECT id
                    FROM "user".users
                    WHERE status = 'deleted'
                      AND purge_after IS NOT NULL
                      AND purge_after <= now()
                    ORDER BY purge_after
                    LIMIT $1
                    FOR UPDATE SKIP LOCKED
                )
                DELETE FROM "user".users AS users
                USING expired
                WHERE users.id = expired.id
                "#,
        )
        .bind(limit)
        .execute(pool)
        .await
        .map_err(|e| {
            tracing::error!(error = %e, "hard delete expired failed");
            AppError::Internal
        })?;
        Ok(res.rows_affected())
    }
}
