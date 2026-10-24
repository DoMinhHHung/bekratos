mod common;

use identity::infra::jwt::JwtSigner;
use identity::infra::key_crypto::KeyEncryptor;
use identity::modules::jwks::repo::JwksRepo;
use identity::modules::jwks::service::{
    create_and_activate_key, decrypt_private_pem, ensure_active_key,
};

#[tokio::test]
async fn database_key_bootstrap_and_rotation_preserve_publishable_overlap() {
    let Some(pool) = common::test_pool().await else {
        eprintln!("DATABASE_URL_TEST not set; skipping JWKS lifecycle test");
        return;
    };
    let encryption_key = hex::encode(rand::random::<[u8; 32]>());
    let previous_active = JwksRepo::find_active(&pool)
        .await
        .expect("find preexisting active key");
    let initial = ensure_active_key(&pool, &encryption_key)
        .await
        .expect("ensure an active signing key");
    assert_eq!(initial.status, "active");

    let encryptor = KeyEncryptor::new(&encryption_key).expect("create test key encryptor");
    let initial_private_pem =
        decrypt_private_pem(&encryptor, &initial).expect("decrypt active private key");
    let initial_signer = JwtSigner::from_pem(
        &initial_private_pem,
        initial.kid.clone(),
        "phase10-issuer".into(),
        "phase10-audience".into(),
        300,
    )
    .expect("build signer from stored key");
    assert_eq!(initial_signer.kid(), initial.kid);

    let rotated = create_and_activate_key(&pool, &encryption_key)
        .await
        .expect("rotate active signing key");
    assert_ne!(rotated.id, initial.id);
    assert_eq!(rotated.status, "active");

    let initial_after_rotation = JwksRepo::find_by_kid(&pool, &initial.kid)
        .await
        .expect("read previous key")
        .expect("previous key exists");
    assert_eq!(initial_after_rotation.status, "retiring");
    assert!(initial_after_rotation.publish_until.is_some());

    let publishable = JwksRepo::list_publishable(&pool)
        .await
        .expect("list overlapping public keys");
    assert!(publishable.iter().any(|key| key.id == initial.id));
    assert!(publishable.iter().any(|key| key.id == rotated.id));

    sqlx::query(
        "UPDATE jwks.signing_keys SET publish_until = now() - interval '1 second' WHERE id = $1",
    )
    .bind(initial.id)
    .execute(&pool)
    .await
    .expect("expire previous key publication window");
    let publishable_after_expiry = JwksRepo::list_publishable(&pool)
        .await
        .expect("list keys after overlap expiry");
    assert!(!publishable_after_expiry
        .iter()
        .any(|key| key.id == initial.id));
    assert!(publishable_after_expiry
        .iter()
        .any(|key| key.id == rotated.id));

    sqlx::query("DELETE FROM jwks.signing_keys WHERE id = $1")
        .bind(rotated.id)
        .execute(&pool)
        .await
        .expect("remove rotated test key");
    if previous_active.is_some() {
        sqlx::query(
            r#"
            UPDATE jwks.signing_keys
            SET status = 'active', retire_at = NULL, publish_until = NULL
            WHERE id = $1
            "#,
        )
        .bind(initial.id)
        .execute(&pool)
        .await
        .expect("restore preexisting active key");
    } else {
        sqlx::query("DELETE FROM jwks.signing_keys WHERE id = $1")
            .bind(initial.id)
            .execute(&pool)
            .await
            .expect("remove bootstrapped test key");
    }
}
