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

pub fn test_database_url() -> Option<String> {
    load_test_env();

    env::var("DATABASE_URL_TEST").ok().filter(|u| !u.is_empty())
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
