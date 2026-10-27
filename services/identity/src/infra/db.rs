use sqlx::postgres::{PgPool, PgPoolOptions};
use std::time::Duration;

use crate::error::{AppError, AppResult};

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

pub async fn migrate(pool: &PgPool) -> AppResult<()> {
    sqlx::migrate!("./migrations").run(pool).await.map_err(|e| {
        tracing::error!(error = %e, "database migration failed");
        AppError::Internal
    })
}

pub async fn ping(pool: &PgPool) -> AppResult<()> {
    sqlx::query("SELECT 1").execute(pool).await.map_err(|e| {
        tracing::error!(error = %e, "database ping failed");
        AppError::Unavailable
    })?;
    Ok(())
}
