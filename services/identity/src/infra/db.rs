use sqlx::postgres::{PgPool, PgPoolOptions};
use std::time::Duration;

use crate::error::{AppError, AppResult};

/// Mở pool PostgreSQL tối đa 10 kết nối, chờ lấy kết nối tối đa 5 giây
/// và đặt thời gian nhàn rỗi 300 giây; trả `Unavailable` nếu kết nối thất bại.
pub async fn connect(database_url: &str) -> AppResult<PgPool> {
    PgPoolOptions::new()
        .max_connections(10)
        .acquire_timeout(Duration::from_secs(5))
        .idle_timeout(Duration::from_secs(300))
        .connect(database_url)
        .await
        .map_err(|e| {
            tracing::error!(error = %e, "database connection failed");
            AppError::Unavailable
        })
}

/// Áp dụng các migration nhúng vào database; trả `Internal` nếu migration lỗi.
pub async fn migrate(pool: &PgPool) -> AppResult<()> {
    sqlx::migrate!("./migrations").run(pool).await.map_err(|e| {
        tracing::error!(error = %e, "database migration failed");
        AppError::Internal
    })
}

/// Kiểm tra database bằng truy vấn đơn giản; trả `Unavailable` nếu truy vấn lỗi.
pub async fn ping(pool: &PgPool) -> AppResult<()> {
    sqlx::query("SELECT 1").execute(pool).await.map_err(|e| {
        tracing::error!(error = %e, "database ping failed");
        AppError::Unavailable
    })?;
    Ok(())
}
