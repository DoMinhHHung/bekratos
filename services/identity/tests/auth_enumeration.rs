mod common;

use identity::config::Config;
use identity::infra::jwt::{public_pem_from_private_pem, JwtSigner, JwtVerifier};
use identity::infra::password::PasswordPolicy;
use identity::infra::redis;
use identity::infra::tokens::{HmacKey, MagicLinkSigner};
use identity::modules::auth::service;
use identity::state::AppState;
use p256::ecdsa::SigningKey;
use p256::pkcs8::{EncodePrivateKey, LineEnding};
use rand::rngs::OsRng;
use sqlx::PgPool;
use std::time::Duration;
use uuid::Uuid;

async fn test_state(pool: PgPool) -> Option<AppState> {
    common::load_test_env();
    let redis_url = common::test_redis_url()?;
    let manager = redis::connect(&redis_url)
        .await
        .expect("connect to REDIS_URL_TEST");

    let signing_key = SigningKey::random(&mut OsRng);
    let private_pem = signing_key
        .to_pkcs8_pem(LineEnding::LF)
        .expect("encode test signing key");
    let public_pem =
        public_pem_from_private_pem(&private_pem).expect("derive test verification key");
    let jwt_signer = JwtSigner::from_pem(
        &private_pem,
        "test-signing-key".into(),
        "test-issuer".into(),
        "test-audience".into(),
        900,
    )
    .expect("build test JWT signer");
    let jwt_verifier = JwtVerifier::new("test-issuer".into(), "test-audience".into());
    jwt_verifier
        .insert_key("test-signing-key".into(), &public_pem)
        .await
        .expect("insert test verification key");
    let magic_link_signer = MagicLinkSigner::new(
        vec![HmacKey {
            kid: "test-hmac".into(),
            secret: vec![42; 32],
        }],
        "test-hmac".into(),
    )
    .expect("build test magic-link signer");
    let config = Config {
        host: "127.0.0.1".into(),
        port: 0,
        bind_addr: "127.0.0.1:0".parse().expect("test bind address"),
        database_url: common::test_database_url().expect("test database URL"),
        redis_url,
        request_timeout: Duration::from_secs(30),
        body_limit_bytes: 1_048_576,
        readiness_require_db: true,
        readiness_require_redis: true,
        magic_link_hmac_keys: "test-hmac".into(),
        magic_link_active_kid: "test-hmac".into(),
        app_public_base_url: "https://example.test".into(),
        app_env: "test".into(),
        mail_provider: "console".into(),
        mail_from: "test@example.test".into(),
        mail_api_key: String::new(),
        mail_timeout_secs: 10,
        smtp_host: "smtp.gmail.com".into(),
        smtp_port: 465,
        smtp_username: String::new(),
        smtp_app_password: String::new(),
        control_plane_service_token: "test-control-plane-service-token-32-characters".into(),
        argon2_memory_kib: 19_456,
        argon2_iterations: 2,
        argon2_parallelism: 1,
        argon2_max_concurrent: 2,
        jwt_signing_key_pem: private_pem.to_string(),
        jwt_kid: "test-signing-key".into(),
        jwt_issuer: "test-issuer".into(),
        jwt_audience: "test-audience".into(),
        jwt_access_ttl_secs: 900,
        jwt_key_encryption_key: "test-key-encryption-material-00000000".into(),
        jwks_cache_ttl_secs: 60,
        refresh_ttl_secs: 3600,
        session_ttl_secs: 3600,
    };
    let password_policy = PasswordPolicy::new(19_456, 2, 1).expect("build test password policy");

    Some(AppState::new(
        config,
        pool,
        manager,
        password_policy,
        magic_link_signer,
        jwt_signer,
        jwt_verifier,
    ))
}

async fn insert_user_with_email(pool: &PgPool, status: &str) -> (Uuid, String) {
    let user_id = Uuid::now_v7();
    let email = format!("phase10-{}@example.test", Uuid::now_v7());
    sqlx::query(r#"INSERT INTO "user".users (id, status) VALUES ($1, $2)"#)
        .bind(user_id)
        .bind(status)
        .execute(pool)
        .await
        .expect("insert enumeration test user");
    sqlx::query(
        r#"
        INSERT INTO "user".user_emails (id, user_id, email, is_primary, verified_at)
        VALUES ($1, $2, $3, true, now())
        "#,
    )
    .bind(Uuid::now_v7())
    .bind(user_id)
    .bind(&email)
    .execute(pool)
    .await
    .expect("insert enumeration test email");
    (user_id, email)
}

#[tokio::test]
async fn recovery_requests_return_identical_messages_for_known_and_unknown_accounts() {
    let Some(pool) = common::test_pool().await else {
        eprintln!("DATABASE_URL_TEST not set; skipping account enumeration test");
        return;
    };
    let Some(state) = test_state(pool.clone()).await else {
        eprintln!("REDIS_URL_TEST unavailable; skipping account enumeration test");
        return;
    };

    let (active_user, active_email) = insert_user_with_email(&pool, "active").await;
    let (pending_user, pending_email) = insert_user_with_email(&pool, "pending").await;
    let (deleted_user, deleted_email) = insert_user_with_email(&pool, "active").await;
    sqlx::query(
        r#"
        UPDATE "user".users
        SET status = 'deleted',
            status_before_deletion = 'active',
            deletion_requested_at = now(),
            purge_after = now() + interval '30 days'
        WHERE id = $1
        "#,
    )
    .bind(deleted_user)
    .execute(&pool)
    .await
    .expect("set valid deleted-account state");

    let absent_email = format!("phase10-absent-{}@example.test", Uuid::now_v7());
    let forgot_absent = service::forgot_password(&state, "198.51.100.31", &absent_email)
        .await
        .expect("forgot password for absent account");
    let forgot_active = service::forgot_password(&state, "198.51.100.31", &active_email)
        .await
        .expect("forgot password for active account");
    let forgot_pending = service::forgot_password(&state, "198.51.100.31", &pending_email)
        .await
        .expect("forgot password for pending account");
    assert_eq!(forgot_absent, forgot_active);
    assert_eq!(forgot_absent, forgot_pending);

    let restore_absent = service::request_restore(&state, "198.51.100.32", &absent_email)
        .await
        .expect("restore request for absent account");
    let restore_active = service::request_restore(&state, "198.51.100.32", &active_email)
        .await
        .expect("restore request for active account");
    let restore_deleted = service::request_restore(&state, "198.51.100.32", &deleted_email)
        .await
        .expect("restore request for deleted account");
    assert_eq!(restore_absent, restore_active);
    assert_eq!(restore_absent, restore_deleted);

    for user_id in [active_user, pending_user, deleted_user] {
        sqlx::query("DELETE FROM audit.security_events WHERE user_id = $1")
            .bind(user_id)
            .execute(&pool)
            .await
            .expect("delete enumeration test audit events");
    }
    for user_id in [active_user, pending_user, deleted_user] {
        sqlx::query(r#"DELETE FROM "user".users WHERE id = $1"#)
            .bind(user_id)
            .execute(&pool)
            .await
            .expect("delete enumeration test user");
    }
}
