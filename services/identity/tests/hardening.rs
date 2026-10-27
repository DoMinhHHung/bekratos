mod common;

use chrono::{Duration as ChronoDuration, Utc};
use identity::error::AppError;
use identity::infra::redis;
use identity::infra::refresh_token::{generate_opaque_token, hash_token};
use identity::middleware::rate_limit::RateLimiter;
use identity::modules::session::repo::{RefreshTokenRepo, SessionRepo};
use identity::modules::token::repo::TokenRepo;
use identity::modules::user::repo::UserRepo;
use sqlx::PgPool;
use tokio::sync::Barrier;
use uuid::Uuid;

async fn create_user(pool: &PgPool, status: &str) -> Uuid {
    let user_id = Uuid::now_v7();
    sqlx::query(r#"INSERT INTO "user".users (id, status) VALUES ($1, $2)"#)
        .bind(user_id)
        .bind(status)
        .execute(pool)
        .await
        .expect("insert test user");
    user_id
}

async fn delete_user(pool: &PgPool, user_id: Uuid) {
    sqlx::query(r#"DELETE FROM "user".users WHERE id = $1"#)
        .bind(user_id)
        .execute(pool)
        .await
        .expect("delete test user");
}

#[tokio::test]
async fn token_consume_is_atomic_under_concurrent_requests() {
    let Some(pool) = common::test_pool().await else {
        eprintln!("DATABASE_URL_TEST not set; skipping token race test");
        return;
    };
    let user_id = create_user(&pool, "pending").await;
    let token_id = Uuid::now_v7();
    TokenRepo::new(&pool)
        .insert(
            token_id,
            user_id,
            "email_verify",
            "phase10@example.test",
            Utc::now() + ChronoDuration::hours(1),
        )
        .await
        .expect("insert test token");

    let barrier = std::sync::Arc::new(Barrier::new(2));
    let tasks = (0..2)
        .map(|_| {
            let pool = pool.clone();
            let barrier = barrier.clone();
            tokio::spawn(async move {
                let mut tx = pool.begin().await.expect("begin consume transaction");
                barrier.wait().await;
                let consumed = TokenRepo::consume_tx(&mut tx, token_id, "email_verify")
                    .await
                    .expect("consume token")
                    .is_some();
                tx.commit().await.expect("commit consume transaction");
                consumed
            })
        })
        .collect::<Vec<_>>();

    let mut successful_consumes = 0;
    for task in tasks {
        successful_consumes += usize::from(task.await.expect("join consume task"));
    }
    assert_eq!(successful_consumes, 1);
    delete_user(&pool, user_id).await;
}

#[tokio::test]
async fn duplicate_email_insert_race_is_stopped_by_database_constraint() {
    let Some(pool) = common::test_pool().await else {
        eprintln!("DATABASE_URL_TEST not set; skipping duplicate email race test");
        return;
    };
    let user_ids = [Uuid::now_v7(), Uuid::now_v7()];
    for user_id in user_ids {
        sqlx::query(r#"INSERT INTO "user".users (id, status) VALUES ($1, 'pending')"#)
            .bind(user_id)
            .execute(&pool)
            .await
            .expect("insert test user");
    }

    let email = format!("phase10-{}@example.test", Uuid::now_v7());
    let barrier = std::sync::Arc::new(Barrier::new(2));
    let tasks = user_ids.map(|user_id| {
        let pool = pool.clone();
        let barrier = barrier.clone();
        let email = email.clone();
        tokio::spawn(async move {
            let mut tx = pool.begin().await.expect("begin email insert");
            barrier.wait().await;
            let result = sqlx::query(
                r#"
                INSERT INTO "user".user_emails (id, user_id, email, is_primary)
                VALUES ($1, $2, $3, true)
                "#,
            )
            .bind(Uuid::now_v7())
            .bind(user_id)
            .bind(email)
            .execute(&mut *tx)
            .await;
            if result.is_ok() {
                tx.commit().await.expect("commit email insert");
                true
            } else {
                tx.rollback().await.expect("rollback duplicate email");
                false
            }
        })
    });

    let mut successes = 0;
    for task in tasks {
        if task.await.expect("join email insert task") {
            successes += 1;
        }
    }
    assert_eq!(successes, 1);
    for user_id in user_ids {
        delete_user(&pool, user_id).await;
    }
}

#[tokio::test]
async fn duplicate_username_insert_race_is_stopped_by_database_constraint() {
    let Some(pool) = common::test_pool().await else {
        eprintln!("DATABASE_URL_TEST not set; skipping duplicate username race test");
        return;
    };
    let user_ids = [Uuid::now_v7(), Uuid::now_v7()];
    for user_id in user_ids {
        sqlx::query(r#"INSERT INTO "user".users (id, status) VALUES ($1, 'pending')"#)
            .bind(user_id)
            .execute(&pool)
            .await
            .expect("insert test user");
    }

    let suffix = Uuid::now_v7()
        .simple()
        .to_string()
        .chars()
        .take(20)
        .collect::<String>();
    let username = format!("p10_{suffix}");
    let barrier = std::sync::Arc::new(Barrier::new(2));
    let tasks = user_ids.map(|user_id| {
        let pool = pool.clone();
        let barrier = barrier.clone();
        let username = username.clone();
        tokio::spawn(async move {
            let mut tx = pool.begin().await.expect("begin username insert");
            barrier.wait().await;
            let result =
                UserRepo::insert_username_tx(&mut tx, Uuid::now_v7(), user_id, &username).await;
            if result.is_ok() {
                tx.commit().await.expect("commit username insert");
                true
            } else {
                tx.rollback().await.expect("rollback duplicate username");
                false
            }
        })
    });

    let mut successes = 0;
    for task in tasks {
        if task.await.expect("join username insert task") {
            successes += 1;
        }
    }
    assert_eq!(successes, 1);
    for user_id in user_ids {
        delete_user(&pool, user_id).await;
    }
}

#[tokio::test]
async fn concurrent_refresh_rotation_creates_one_child_and_revokes_on_reuse() {
    let Some(pool) = common::test_pool().await else {
        eprintln!("DATABASE_URL_TEST not set; skipping refresh rotation race test");
        return;
    };
    let user_id = create_user(&pool, "active").await;
    let session_id = Uuid::now_v7();
    sqlx::query(
        "INSERT INTO session.sessions (id, user_id, expires_at) VALUES ($1, $2, now() + interval '1 hour')",
    )
    .bind(session_id)
    .bind(user_id)
    .execute(&pool)
    .await
    .expect("insert refresh test session");

    let parent_id = Uuid::now_v7();
    let parent_token = generate_opaque_token();
    let parent_hash = hash_token(&parent_token);
    let mut tx = pool.begin().await.expect("begin root refresh insert");
    RefreshTokenRepo::insert_tx(
        &mut tx,
        parent_id,
        session_id,
        None,
        &parent_hash,
        Utc::now() + ChronoDuration::hours(1),
    )
    .await
    .expect("insert root refresh token");
    tx.commit().await.expect("commit root refresh token");

    let barrier = std::sync::Arc::new(Barrier::new(2));
    let tasks = (0..2)
        .map(|_| {
            let pool = pool.clone();
            let barrier = barrier.clone();
            let parent_hash = parent_hash.clone();
            tokio::spawn(async move {
                barrier.wait().await;
                let mut tx = pool.begin().await.expect("begin refresh rotation");
                let parent = RefreshTokenRepo::find_by_hash_for_update_tx(&mut tx, &parent_hash)
                    .await
                    .expect("lock parent refresh token")
                    .expect("parent refresh token exists");

                if parent.used_at.is_some() || parent.revoked_at.is_some() {
                    SessionRepo::revoke_tx(&mut tx, parent.session_id, "refresh_reuse")
                        .await
                        .expect("revoke reused session");
                    RefreshTokenRepo::revoke_all_for_session_tx(&mut tx, parent.session_id)
                        .await
                        .expect("revoke refresh-token family");
                    tx.commit().await.expect("commit refresh reuse revocation");
                    return false;
                }

                RefreshTokenRepo::mark_used_tx(&mut tx, parent.id)
                    .await
                    .expect("mark parent used");
                let child_token = generate_opaque_token();
                RefreshTokenRepo::insert_tx(
                    &mut tx,
                    Uuid::now_v7(),
                    parent.session_id,
                    Some(parent.id),
                    &hash_token(&child_token),
                    Utc::now() + ChronoDuration::hours(1),
                )
                .await
                .expect("insert rotated child");
                tx.commit().await.expect("commit refresh rotation");
                true
            })
        })
        .collect::<Vec<_>>();

    let mut successful_rotations = 0;
    for task in tasks {
        successful_rotations += usize::from(task.await.expect("join refresh rotation"));
    }
    assert_eq!(successful_rotations, 1);

    let (child_count, active_child_count): (i64, i64) = sqlx::query_as(
        r#"
        SELECT COUNT(*), COUNT(*) FILTER (WHERE used_at IS NULL AND revoked_at IS NULL)
        FROM session.refresh_tokens
        WHERE parent_id = $1
        "#,
    )
    .bind(parent_id)
    .fetch_one(&pool)
    .await
    .expect("count rotated children");
    assert_eq!(child_count, 1);
    assert_eq!(active_child_count, 0);

    let (session_revoked,): (bool,) =
        sqlx::query_as("SELECT revoked_at IS NOT NULL FROM session.sessions WHERE id = $1")
            .bind(session_id)
            .fetch_one(&pool)
            .await
            .expect("check reused session revocation");
    assert!(session_revoked);
    delete_user(&pool, user_id).await;
}

#[tokio::test]
async fn session_revocation_persists_and_revokes_its_refresh_tokens() {
    let Some(pool) = common::test_pool().await else {
        eprintln!("DATABASE_URL_TEST not set; skipping session revocation test");
        return;
    };
    let user_id = create_user(&pool, "active").await;
    let session_id = Uuid::now_v7();
    let refresh_id = Uuid::now_v7();
    let mut tx = pool.begin().await.expect("begin session creation");
    SessionRepo::insert_tx(
        &mut tx,
        session_id,
        user_id,
        Some("192.0.2.11"),
        Some("phase10-test"),
        None,
        Utc::now() + ChronoDuration::hours(1),
    )
    .await
    .expect("insert test session");
    RefreshTokenRepo::insert_tx(
        &mut tx,
        refresh_id,
        session_id,
        None,
        &hash_token(&generate_opaque_token()),
        Utc::now() + ChronoDuration::hours(1),
    )
    .await
    .expect("insert session refresh token");
    tx.commit().await.expect("commit test session");

    let mut tx = pool.begin().await.expect("begin session revocation");
    SessionRepo::revoke_tx(&mut tx, session_id, "phase10_test")
        .await
        .expect("revoke session");
    RefreshTokenRepo::revoke_all_for_session_tx(&mut tx, session_id)
        .await
        .expect("revoke session refresh tokens");
    tx.commit().await.expect("commit session revocation");

    let active_sessions = SessionRepo::new(&pool)
        .list_active_for_user(user_id)
        .await
        .expect("list active sessions");
    assert!(!active_sessions
        .iter()
        .any(|session| session.id == session_id));
    let (refresh_revoked,): (bool,) =
        sqlx::query_as("SELECT revoked_at IS NOT NULL FROM session.refresh_tokens WHERE id = $1")
            .bind(refresh_id)
            .fetch_one(&pool)
            .await
            .expect("check refresh token revocation");
    assert!(refresh_revoked);
    delete_user(&pool, user_id).await;
}

#[tokio::test]
async fn email_worker_claims_are_exclusive_and_expired_leases_are_reclaimed() {
    let Some(pool) = common::test_pool().await else {
        eprintln!("DATABASE_URL_TEST not set; skipping email claim test");
        return;
    };
    let user_id = create_user(&pool, "pending").await;
    let token_id = Uuid::now_v7();
    let email = format!("{}@example.test", Uuid::now_v7());
    TokenRepo::new(&pool)
        .insert(
            token_id,
            user_id,
            "email_verify",
            &email,
            Utc::now() + ChronoDuration::hours(1),
        )
        .await
        .expect("insert delivery token");

    let barrier = std::sync::Arc::new(Barrier::new(2));
    let tasks = (0..2)
        .map(|_| {
            let pool = pool.clone();
            let barrier = barrier.clone();
            tokio::spawn(async move {
                barrier.wait().await;
                TokenRepo::new(&pool)
                    .claim_due_for_delivery(10, 60)
                    .await
                    .expect("claim due email")
            })
        })
        .collect::<Vec<_>>();
    let mut claimed_count = 0;
    for task in tasks {
        claimed_count += task
            .await
            .expect("join email claim")
            .into_iter()
            .filter(|row| row.id == token_id)
            .count();
    }
    assert_eq!(claimed_count, 1);

    sqlx::query(
        "UPDATE token.verification_tokens SET lease_until = now() - interval '1 second' WHERE id = $1",
    )
    .bind(token_id)
    .execute(&pool)
    .await
    .expect("expire email lease");
    let reclaimed = TokenRepo::new(&pool)
        .claim_due_for_delivery(10, 60)
        .await
        .expect("reclaim expired lease")
        .into_iter()
        .find(|row| row.id == token_id)
        .expect("reclaimed token");
    assert_eq!(reclaimed.delivery_attempts, 2);
    delete_user(&pool, user_id).await;
}

#[tokio::test]
async fn restore_preserves_suspended_status_and_rejects_expired_window() {
    let Some(pool) = common::test_pool().await else {
        eprintln!("DATABASE_URL_TEST not set; skipping restore test");
        return;
    };
    let suspended_user = create_user(&pool, "suspended").await;
    let mut tx = pool.begin().await.expect("begin soft delete");
    UserRepo::soft_delete_tx(
        &mut tx,
        suspended_user,
        Utc::now() + ChronoDuration::days(30),
    )
    .await
    .expect("soft delete suspended account");
    tx.commit().await.expect("commit soft delete");

    let mut tx = pool.begin().await.expect("begin restore");
    UserRepo::restore_tx(&mut tx, suspended_user)
        .await
        .expect("restore within window");
    tx.commit().await.expect("commit restore");

    let (status,): (String,) = sqlx::query_as(r#"SELECT status FROM "user".users WHERE id = $1"#)
        .bind(suspended_user)
        .fetch_one(&pool)
        .await
        .expect("read restored status");
    assert_eq!(status, "suspended");
    delete_user(&pool, suspended_user).await;

    let expired_user = create_user(&pool, "active").await;
    let mut tx = pool.begin().await.expect("begin expired soft delete");
    UserRepo::soft_delete_tx(&mut tx, expired_user, Utc::now() + ChronoDuration::days(1))
        .await
        .expect("soft delete expired account");
    sqlx::query(
        r#"
        UPDATE "user".users
        SET purge_after = now() - interval '1 second'
        WHERE id = $1
        "#,
    )
    .bind(expired_user)
    .execute(&mut *tx)
    .await
    .expect("expire account using database clock");
    tx.commit().await.expect("commit expired soft delete");

    let mut tx = pool.begin().await.expect("begin expired restore");
    let restore_result = UserRepo::restore_tx(&mut tx, expired_user).await;
    assert!(
        matches!(restore_result, Err(AppError::BadRequest(_))),
        "expired restore result: {restore_result:?}"
    );
    tx.rollback().await.expect("rollback rejected restore");
    delete_user(&pool, expired_user).await;
}

#[tokio::test]
async fn expired_account_purge_obeys_batch_limit_and_is_idempotent() {
    let Some(pool) = common::test_pool().await else {
        eprintln!("DATABASE_URL_TEST not set; skipping purge test");
        return;
    };
    let user_ids = [
        create_user(&pool, "active").await,
        create_user(&pool, "active").await,
        create_user(&pool, "active").await,
    ];
    sqlx::query(
        r#"
        UPDATE "user".users
        SET status = 'deleted',
            status_before_deletion = 'active',
            deletion_requested_at = now() - interval '31 days',
            purge_after = now() - interval '1 day'
        WHERE id = ANY($1)
        "#,
    )
    .bind(&user_ids[..])
    .execute(&pool)
    .await
    .expect("expire test accounts");

    UserRepo::hard_delete_expired(&pool, 1)
        .await
        .expect("purge one expired account");
    let (remaining_after_batch,): (i64,) = sqlx::query_as(
        r#"SELECT COUNT(*) FROM "user".users WHERE id = ANY($1) AND status = 'deleted'"#,
    )
    .bind(&user_ids[..])
    .fetch_one(&pool)
    .await
    .expect("count accounts after batch");
    assert_eq!(remaining_after_batch, 2);

    UserRepo::hard_delete_expired(&pool, 10)
        .await
        .expect("purge remaining expired accounts");
    UserRepo::hard_delete_expired(&pool, 10)
        .await
        .expect("repeat empty purge");
    let (remaining_after_purge,): (i64,) =
        sqlx::query_as(r#"SELECT COUNT(*) FROM "user".users WHERE id = ANY($1)"#)
            .bind(&user_ids[..])
            .fetch_one(&pool)
            .await
            .expect("count accounts after purge");
    assert_eq!(remaining_after_purge, 0);
}

#[tokio::test]
async fn redis_rate_limit_enforces_limit_and_fails_closed() {
    common::load_test_env();
    let Some(redis_url) = common::test_redis_url() else {
        eprintln!("REDIS_URL_TEST not set; skipping Redis rate-limit test");
        return;
    };
    let manager = redis::connect(&redis_url)
        .await
        .expect("connect to REDIS_URL_TEST");
    let limiter = RateLimiter::new(manager);
    let endpoint = format!("phase10-{}", Uuid::now_v7());

    limiter
        .check_and_incr(&endpoint, "ip", "198.51.100.10", 1, 60)
        .await
        .expect("first request within limit");
    assert!(matches!(
        limiter
            .check_and_incr(&endpoint, "ip", "198.51.100.10", 1, 60)
            .await,
        Err(AppError::TooManyRequests)
    ));

    let unavailable = match redis::connect("redis://127.0.0.1:1").await {
        Ok(_) => panic!("unavailable Redis should fail connection"),
        Err(error) => error,
    };
    assert!(matches!(unavailable, AppError::Unavailable));
}

#[tokio::test]
async fn terminal_email_failures_and_expired_tokens_are_not_reclaimed() {
    let Some(pool) = common::test_pool().await else {
        eprintln!("DATABASE_URL_TEST not set; skipping email terminal-state test");
        return;
    };
    let user_id = create_user(&pool, "pending").await;
    let repo = TokenRepo::new(&pool);
    let permanent_id = Uuid::now_v7();
    repo.insert(
        permanent_id,
        user_id,
        "email_verify",
        "terminal-email@example.test",
        Utc::now() + ChronoDuration::hours(1),
    )
    .await
    .expect("insert permanent-failure token");
    repo.mark_delivery_permanent_failure(permanent_id, "max_attempts")
        .await
        .expect("mark token permanently failed");
    let (retry_after_expiry, is_scheduled_after_expiry): (chrono::DateTime<Utc>, bool) =
        sqlx::query_as(
            "SELECT next_attempt_at, next_attempt_at > now() FROM token.verification_tokens WHERE id = $1",
        )
        .bind(permanent_id)
        .fetch_one(&pool)
        .await
        .expect("read terminal token state");
    let expires_at: chrono::DateTime<Utc> = sqlx::query_scalar(
        "SELECT expires_at FROM token.verification_tokens WHERE id = $1",
    )
    .bind(permanent_id)
    .fetch_one(&pool)
    .await
    .expect("read token expiry");
    assert_eq!(retry_after_expiry, expires_at);
    assert!(is_scheduled_after_expiry);

    let expired_id = Uuid::now_v7();
    repo.insert(
        expired_id,
        user_id,
        "email_verify",
        "expired-email@example.test",
        Utc::now() + ChronoDuration::hours(1),
    )
    .await
    .expect("insert token for expiry test");
    sqlx::query(
        "UPDATE token.verification_tokens SET created_at = now() - interval '2 minutes', expires_at = now() - interval '1 minute' WHERE id = $1",
    )
    .bind(expired_id)
    .execute(&pool)
    .await
    .expect("expire token after creation");
    let expired_claim = repo.claim_due_for_delivery(10, 60).await.expect("claim expired token");
    assert!(expired_claim.is_empty());
    delete_user(&pool, user_id).await;
}

#[tokio::test]
async fn email_queue_state_updates_fail_when_token_row_is_missing() {
    let Some(pool) = common::test_pool().await else {
        eprintln!("DATABASE_URL_TEST not set; skipping email queue persistence test");
        return;
    };
    let repo = TokenRepo::new(&pool);
    let missing_id = Uuid::now_v7();
    assert!(repo.mark_sent(missing_id).await.is_err());
    assert!(repo.mark_delivery_failure(missing_id, "send_failed", 30).await.is_err());
    assert!(repo.mark_delivery_permanent_failure(missing_id, "max_attempts").await.is_err());
}

#[tokio::test]
async fn sent_tokens_are_not_reclaimed_after_mark_sent() {
    let Some(pool) = common::test_pool().await else {
        eprintln!("DATABASE_URL_TEST not set; skipping mark_sent claim test");
        return;
    };
    let user_id = create_user(&pool, "pending").await;
    let token_id = Uuid::now_v7();
    let email = format!("{}@example.test", Uuid::now_v7());
    let repo = TokenRepo::new(&pool);
    repo.insert(
        token_id,
        user_id,
        "email_verify",
        &email,
        Utc::now() + ChronoDuration::hours(1),
    )
    .await
    .expect("insert token");

    let claimed = repo
        .claim_due_for_delivery(10, 60)
        .await
        .expect("claim")
        .into_iter()
        .find(|row| row.id == token_id)
        .expect("token claimed once");
    assert!(claimed.sent_at.is_none());

    repo.mark_sent(token_id).await.expect("mark sent");

    let again = repo
        .claim_due_for_delivery(10, 60)
        .await
        .expect("claim after sent");
    assert!(
        again.iter().all(|row| row.id != token_id),
        "successfully sent tokens must not be reclaimed"
    );
    delete_user(&pool, user_id).await;
}
