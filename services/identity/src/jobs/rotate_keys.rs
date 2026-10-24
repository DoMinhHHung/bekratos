use std::time::Duration;

use sqlx::PgPool;
use tokio::sync::watch;

use crate::modules::jwks::repo::JwksRepo;

pub struct KeyReconcileWorker {
    pool: PgPool,
    interval: Duration,
}

impl KeyReconcileWorker {
    /// Tạo worker đối soát trạng thái khóa với chu kỳ 300 giây.
    pub fn new(pool: PgPool) -> Self {
        Self {
            pool,
            interval: Duration::from_secs(300),
        }
    }

    /// Định kỳ chuyển khóa retiring hết thời gian công bố sang retired.
    ///
    /// Tiếp tục khi cập nhật lỗi và dừng khi nhận thay đổi giá trị shutdown thành
    /// `true`; worker không tạo khóa mới hay cập nhật bộ xác minh JWT trong bộ nhớ.
    pub async fn run(self, mut shutdown: watch::Receiver<bool>) {
        loop {
            tokio::select! {
                _ = shutdown.changed() => {
                    if *shutdown.borrow() {
                        break;
                    }
                }
                _ = tokio::time::sleep(self.interval) => {
                    match JwksRepo::mark_retired_expired(&self.pool).await {
                        Ok(n) if n > 0 => tracing::info!(retired = n, "retired expired signing keys"),
                        Ok(_) => {},
                        Err(e) => tracing::error!(error = %e, "key reconcile failed"),
                    }
                }
            }
        }
    }
}
