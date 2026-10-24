use std::time::Duration;

use redis::aio::ConnectionManager;
use redis::Client;

use crate::error::{AppError, AppResult};

/// Tạo kết nối Redis có quản lý với thời gian chờ khởi tạo tối đa 5 giây.
///
/// Trả `Unavailable` khi URL không hợp lệ, kết nối lỗi hoặc hết thời gian chờ.
pub async fn connect(redis_url: &str) -> AppResult<ConnectionManager> {
    let client = Client::open(redis_url).map_err(|e| {
        tracing::error!(error = %e, "redis client open failed");
        AppError::Unavailable
    })?;

    match tokio::time::timeout(Duration::from_secs(5), ConnectionManager::new(client)).await {
        Ok(Ok(manager)) => Ok(manager),
        Ok(Err(e)) => {
            tracing::error!(error = %e, "redis connection manager failed");
            Err(AppError::Unavailable)
        }
        Err(_) => {
            tracing::error!("redis connect timed out");
            Err(AppError::Unavailable)
        }
    }
}

/// Kiểm tra Redis bằng PING; trả `Unavailable` khi lệnh lỗi hoặc phản hồi khác PONG.
pub async fn ping(conn: &mut ConnectionManager) -> AppResult<()> {
    let pong: String = redis::cmd("PING").query_async(conn).await.map_err(|e| {
        tracing::error!(error = %e, "redis ping failed");
        AppError::Unavailable
    })?;

    if pong != "PONG" {
        tracing::error!(%pong, "unexpected redis ping response");
        return Err(AppError::Unavailable);
    }

    Ok(())
}
