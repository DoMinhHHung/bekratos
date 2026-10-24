use std::env;
use std::path::PathBuf;
use std::time::Duration;

use sqlx::postgres::PgPoolOptions;
use sqlx::PgPool;

pub fn load_test_env() {
    let candidates = [
        PathBuf::from(".env"),
        PathBuf::from("services/identity/.env"),
        PathBuf::from("../.env"),
    ];

    for path in candidates {
        if path.exists() {
            let _ = dotenvy::from_path(&path);
            break;
        }
    }
}

fn require_or_skip(name: &str) -> Option<String> {
    load_test_env();
    match env::var(name) {
        Ok(v) if !v.is_empty() => Some(v),
        _ => {
            if env::var("CI").ok().as_deref() == Some("true") {
                panic!("{name} must be set when CI=true");
            }
            None
        }
    }
}

pub fn test_database_url() -> Option<String> {
    require_or_skip("DATABASE_URL_TEST")
}

#[allow(dead_code)]
pub fn test_redis_url() -> Option<String> {
    require_or_skip("REDIS_URL_TEST")
}

pub async fn test_pool() -> Option<PgPool> {
    let database_url = test_database_url()?;
    let pool = PgPoolOptions::new()
        .max_connections(10)
        .acquire_timeout(Duration::from_secs(10))
        .connect(&database_url)
        .await
        .expect("connect to DATABASE_URL_TEST");
    sqlx::migrate!("./migrations")
        .run(&pool)
        .await
        .expect("migrate DATABASE_URL_TEST");
    Some(pool)
}
