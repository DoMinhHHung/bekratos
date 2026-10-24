mod common;

use identity::modules::user::repo::UserRepo;
use uuid::Uuid;

#[tokio::test]
async fn insert_and_find_pending_user() {
    let pool = match common::test_pool().await {
        Some(pool) => pool,
        None => {
            eprintln!("DATABASE_URL_TEST not set in .env; skipping user_repo test");
            return;
        }
    };

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
