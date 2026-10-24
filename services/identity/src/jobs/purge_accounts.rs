use std::time::Duration;

use sqlx::PgPool;
use tokio::sync::watch;
use tracing;

use crate::modules::user::repo::UserRepo;

pub struct PurgeWorker {
    pool: PgPool,
    interval: Duration,
    batch_limit: i64,
}

impl PurgeWorker {
    /// Tạo tác vụ xóa vĩnh viễn tài khoản hết hạn khôi phục, tối đa 100 tài khoản mỗi giờ.
    pub fn new(pool: PgPool) -> Self {
        Self {
            pool,
            interval: Duration::from_secs(3600),
            batch_limit: 100,
        }
    }

    /// Chạy từng lô purge sau mỗi khoảng chờ và tiếp tục khi cơ sở dữ liệu báo lỗi.
    /// Dừng khi nhận thay đổi `shutdown` thành `true`; lỗi purge không trả cho bên gọi.
    pub async fn run(self, mut shutdown: watch::Receiver<bool>) {
        loop {
            tokio::select! {
                _ = shutdown.changed() => {
                    if *shutdown.borrow() {
                        tracing::info!("purge worker shutting down");
                        break;
                    }
                }
                _ = tokio::time::sleep(self.interval) => {
                    match UserRepo::hard_delete_expired(&self.pool, self.batch_limit).await {
                        Ok(n) if n > 0 => tracing::info!(purged = n, "purged deleted accounts"),
                        Ok(_) => {},
                        Err(e) => tracing::error!(error = %e, "purge failed"),
                    }
                }
            }
        }
    }
}
