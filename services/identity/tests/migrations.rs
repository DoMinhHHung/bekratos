mod common;

use sqlx::postgres::PgPoolOptions;
use std::time::Duration;

#[tokio::test]
async fn migrations_apply_cleanly() {
    let database_url = match common::test_database_url() {
        Some(u) => u,
        None => {
            eprintln!("DATABASE_URL_TEST not set in .env; skipping migration test");
            return;
        }
    };

    let pool = PgPoolOptions::new()
        .max_connections(2)
        .acquire_timeout(Duration::from_secs(10))
        .connect(&database_url)
        .await
        .expect("connect to DATABASE_URL_TEST");

    sqlx::migrate!("./migrations")
        .run(&pool)
        .await
        .expect("migrate");

    let (exists,): (bool,) = sqlx::query_as(
        r#"
        SELECT EXISTS (
            SELECT 1
            FROM information_schema.tables
            WHERE table_schema = 'user' AND table_name = 'users'
        )
        "#,
    )
    .fetch_one(&pool)
    .await
    .expect("check table");

    assert!(exists);
}
