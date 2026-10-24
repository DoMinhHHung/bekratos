use std::time::Duration;

use sqlx::PgPool;
use tokio::sync::watch;

use crate::modules::jwks::repo::JwksRepo;

pub struct KeyReconcileWorker {
    pool: PgPool,
    interval: Duration,
}

impl KeyReconcileWorker {
    pub fn new(pool: PgPool) -> Self {
        Self {
            pool,
            interval: Duration::from_secs(300),
        }
    }

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
