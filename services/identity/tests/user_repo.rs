mod common;

use identity::modules::user::repo::UserRepo;
use sqlx::postgres::PgPoolOptions;
use std::time::Duration;
use uuid::Uuid;

#[tokio::test]
async fn insert_and_find_pending_user() {
    let database_url = match common::test_database_url() {
        Some(u) => u,
        None => {
            eprintln!("DATABASE_URL_TEST not set in .env; skipping user_repo test");
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

    let id = Uuid::now_v7();
    let repo = UserRepo::new(&pool);

    let inserted = repo
        .insert_pending(id, Some("vi"), Some("Asia/Ho_Chi_Minh"))
        .await
        .expect("insert");

    assert_eq!(inserted.id, id);
    assert_eq!(inserted.status, "pending");

    let found = repo.find_by_id(id).await.expect("find").expect("exists");
    assert_eq!(found.id, id);
    assert_eq!(found.status, "pending");
}
