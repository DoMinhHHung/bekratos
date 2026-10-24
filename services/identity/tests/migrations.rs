mod common;

#[tokio::test]
async fn migrations_apply_cleanly() {
    let pool = match common::test_pool().await {
        Some(pool) => pool,
        None => {
            eprintln!("DATABASE_URL_TEST not set in .env; skipping migration test");
            return;
        }
    };

    sqlx::migrate!("./migrations")
        .run(&pool)
        .await
        .expect("reapplying migrations is a no-op");

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
