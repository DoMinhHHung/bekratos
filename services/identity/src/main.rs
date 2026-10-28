use std::net::SocketAddr;
use std::sync::Arc;
use std::time::Duration;

use identity::infra::db;
use identity::infra::jwt::{public_pem_from_private_pem, JwtSigner, JwtVerifier};
use identity::infra::key_crypto::KeyEncryptor;
use identity::infra::mailer::{ConsoleMailer, Mailer, NoopMailer};
use identity::infra::password::PasswordPolicy;
use identity::infra::redis as redis_infra;
use identity::infra::resend::ResendMailer;
use identity::infra::smtp::SmtpMailer;
use identity::infra::tokens::{parse_hmac_keys, MagicLinkSigner};
use identity::jobs::email_worker::{EmailWorker, EmailWorkerConfig};
use identity::jobs::purge_accounts::PurgeWorker;
use identity::jobs::rotate_keys::KeyReconcileWorker;
use identity::modules::jwks::repo::JwksRepo;
use identity::modules::jwks::service::{decrypt_private_pem, ensure_active_key};
use identity::{build_router, AppState, Config};
use tokio::net::TcpListener;
use tokio::sync::watch;
use tracing_subscriber::EnvFilter;

#[tokio::main]
async fn main() {
    tracing_subscriber::fmt()
        .with_env_filter(
            EnvFilter::try_from_default_env().unwrap_or_else(|_| EnvFilter::new("info")),
        )
        .json()
        .init();

    let config = match Config::from_env() {
        Ok(c) => c,
        Err(e) => {
            tracing::error!(error = %e, "failed to load configuration");
            std::process::exit(1);
        }
    };

    let hmac_keys = match parse_hmac_keys(&config.magic_link_hmac_keys) {
        Ok(k) => k,
        Err(e) => {
            tracing::error!(error = %e, "invalid MAGIC_LINK_HMAC_KEYS");
            std::process::exit(1);
        }
    };

    let signer = match MagicLinkSigner::new(hmac_keys, config.magic_link_active_kid.clone()) {
        Ok(s) => s,
        Err(e) => {
            tracing::error!(error = %e, "invalid magic link signer config");
            std::process::exit(1);
        }
    };

    let pool = match db::connect(&config.database_url).await {
        Ok(p) => p,
        Err(e) => {
            tracing::error!(error = %e, "failed to connect to database");
            std::process::exit(1);
        }
    };

    if let Err(e) = db::migrate(&pool).await {
        tracing::error!(error = %e, "failed to run migrations");
        std::process::exit(1);
    }

    let redis = match redis_infra::connect(&config.redis_url).await {
        Ok(r) => r,
        Err(e) => {
            tracing::error!(error = %e, "failed to connect to redis");
            std::process::exit(1);
        }
    };

    let password_policy = match PasswordPolicy::new(
        config.argon2_memory_kib,
        config.argon2_iterations,
        config.argon2_parallelism,
    ) {
        Ok(p) => p,
        Err(e) => {
            tracing::error!(error = %e, "invalid argon2 configuration");
            std::process::exit(1);
        }
    };

    let mailer: Arc<dyn Mailer> = match config.mail_provider.as_str() {
        "console" => Arc::new(ConsoleMailer {
            from: config.mail_from.clone(),
        }),
        "resend" => Arc::new(ResendMailer::new(
            config.mail_from.clone(),
            config.mail_api_key.clone(),
            Duration::from_secs(config.mail_timeout_secs),
        )),
        "smtp" => Arc::new(SmtpMailer::new(
            config.smtp_host.clone(),
            config.smtp_port,
            config.smtp_username.clone(),
            config.smtp_app_password.clone(),
            config.mail_from.clone(),
            Duration::from_secs(config.mail_timeout_secs),
        )),
        _ => Arc::new(NoopMailer),
    };

    let (shutdown_tx, _shutdown_rx) = watch::channel(false);
    let mut worker_handles = Vec::new();

    let signer_for_worker = signer.clone();
    let worker = EmailWorker::new(
        pool.clone(),
        mailer,
        signer_for_worker,
        EmailWorkerConfig {
            poll_interval: Duration::from_secs(5),
            batch_size: 20,
            lease_secs: 60,
            max_attempts: 8,
            public_base_url: config.app_public_base_url.clone(),
        },
    );
    let shutdown_rx_email = shutdown_tx.subscribe();
    worker_handles.push(tokio::spawn(async move {
        worker.run(shutdown_rx_email).await;
    }));

    let purge = PurgeWorker::new(pool.clone());
    let shutdown_rx_purge = shutdown_tx.subscribe();
    worker_handles.push(tokio::spawn(async move {
        purge.run(shutdown_rx_purge).await;
    }));

    let reconcile = KeyReconcileWorker::new(pool.clone());
    let shutdown_rx_reconcile = shutdown_tx.subscribe();
    worker_handles.push(tokio::spawn(async move {
        reconcile.run(shutdown_rx_reconcile).await;
    }));

    let encryptor = match KeyEncryptor::new(&config.jwt_key_encryption_key) {
        Ok(e) => e,
        Err(e) => {
            tracing::error!(error = %e, "invalid JWT_KEY_ENCRYPTION_KEY");
            std::process::exit(1);
        }
    };

    let active_row = match ensure_active_key(&pool, &config.jwt_key_encryption_key).await {
        Ok(r) => r,
        Err(e) => {
            if !config.jwt_signing_key_pem.is_empty() {
                tracing::warn!(
                    error = %e,
                    "DB key bootstrap failed, falling back to JWT_SIGNING_KEY_PEM"
                );
                let jwt_signer = match JwtSigner::from_pem(
                    &config.jwt_signing_key_pem,
                    config.jwt_kid.clone(),
                    config.jwt_issuer.clone(),
                    config.jwt_audience.clone(),
                    config.jwt_access_ttl_secs,
                ) {
                    Ok(s) => s,
                    Err(e2) => {
                        tracing::error!(error = %e2, "invalid JWT_SIGNING_KEY_PEM");
                        std::process::exit(1);
                    }
                };
                let jwt_verifier =
                    JwtVerifier::new(config.jwt_issuer.clone(), config.jwt_audience.clone());
                if let Ok(pub_pem) = public_pem_from_private_pem(&config.jwt_signing_key_pem) {
                    let _ = jwt_verifier
                        .insert_key(config.jwt_kid.clone(), &pub_pem)
                        .await;
                }
                let state = AppState::new(
                    config,
                    pool.clone(),
                    redis,
                    password_policy,
                    signer,
                    jwt_signer,
                    jwt_verifier,
                );
                run_server(state, shutdown_tx, worker_handles, pool).await;
                return;
            }
            tracing::error!(error = %e, "failed to ensure active signing key");
            std::process::exit(1);
        }
    };

    let private_pem = match decrypt_private_pem(&encryptor, &active_row) {
        Ok(p) => p,
        Err(e) => {
            tracing::error!(error = %e, "failed to decrypt active signing key");
            std::process::exit(1);
        }
    };

    let jwt_signer = match JwtSigner::from_pem(
        &private_pem,
        active_row.kid.clone(),
        config.jwt_issuer.clone(),
        config.jwt_audience.clone(),
        config.jwt_access_ttl_secs,
    ) {
        Ok(s) => s,
        Err(e) => {
            tracing::error!(error = %e, "failed to build JwtSigner from active key");
            std::process::exit(1);
        }
    };

    let jwt_verifier = JwtVerifier::new(config.jwt_issuer.clone(), config.jwt_audience.clone());

    match JwksRepo::list_publishable(&pool).await {
        Ok(keys) => {
            for row in keys {
                if let Ok(pub_pem) = jwk_to_public_pem(&row.public_jwk) {
                    if let Err(e) = jwt_verifier.insert_key(row.kid.clone(), &pub_pem).await {
                        tracing::warn!(error = %e, kid = %row.kid, "skip loading verify key");
                    }
                }
            }
        }
        Err(e) => {
            tracing::error!(error = %e, "failed to load publishable keys into verifier");
            std::process::exit(1);
        }
    }

    if let Ok(pub_pem) = public_pem_from_private_pem(&private_pem) {
        let _ = jwt_verifier
            .insert_key(active_row.kid.clone(), &pub_pem)
            .await;
    }

    let state = AppState::new(
        config,
        pool.clone(),
        redis,
        password_policy,
        signer,
        jwt_signer,
        jwt_verifier,
    );

    run_server(state, shutdown_tx, worker_handles, pool).await;
}

async fn run_server(
    state: AppState,
    shutdown_tx: watch::Sender<bool>,
    worker_handles: Vec<tokio::task::JoinHandle<()>>,
    pool: sqlx::PgPool,
) {
    let addr = state.config.bind_addr;
    let app = build_router(state);

    let listener = match TcpListener::bind(addr).await {
        Ok(l) => l,
        Err(e) => {
            tracing::error!(error = %e, %addr, "failed to bind");
            std::process::exit(1);
        }
    };

    tracing::info!(%addr, "identity service listening");

    let server = axum::serve(
        listener,
        app.into_make_service_with_connect_info::<SocketAddr>(),
    )
    .with_graceful_shutdown(async move {
        shutdown_signal().await;
        let _ = shutdown_tx.send(true);
    });

    if let Err(e) = server.await {
        tracing::error!(error = %e, "server error");
        std::process::exit(1);
    }

    let grace = Duration::from_secs(25);
    let join_workers = async {
        for handle in worker_handles {
            let _ = handle.await;
        }
    };
    if tokio::time::timeout(grace, join_workers).await.is_err() {
        tracing::warn!("worker shutdown grace period elapsed");
    }

    pool.close().await;
    tracing::info!("identity service stopped");
}
async fn shutdown_signal() {
    let ctrl_c = async {
        match tokio::signal::ctrl_c().await {
            Ok(()) => {}
            Err(e) => {
                tracing::error!(error = %e, "failed to install Ctrl+C handler");
                std::future::pending::<()>().await;
            }
        }
    };

    #[cfg(unix)]
    let terminate = async {
        match tokio::signal::unix::signal(tokio::signal::unix::SignalKind::terminate()) {
            Ok(mut sig) => {
                sig.recv().await;
            }
            Err(e) => {
                tracing::error!(error = %e, "failed to install SIGTERM handler");
                std::future::pending::<()>().await;
            }
        }
    };

    #[cfg(not(unix))]
    let terminate = std::future::pending::<()>();

    tokio::select! {
        _ = ctrl_c => {},
        _ = terminate => {},
    }

    tracing::info!("shutdown signal received");
}

fn jwk_to_public_pem(jwk: &serde_json::Value) -> Result<String, ()> {
    use base64::engine::general_purpose::URL_SAFE_NO_PAD;
    use base64::Engine;
    use p256::pkcs8::EncodePublicKey;
    use p256::PublicKey;

    let x_b64 = jwk.get("x").and_then(|v| v.as_str()).ok_or(())?;
    let y_b64 = jwk.get("y").and_then(|v| v.as_str()).ok_or(())?;
    let x = URL_SAFE_NO_PAD.decode(x_b64).map_err(|_| ())?;
    let y = URL_SAFE_NO_PAD.decode(y_b64).map_err(|_| ())?;
    if x.len() != 32 || y.len() != 32 {
        return Err(());
    }
    let mut uncompressed = vec![0x04];
    uncompressed.extend_from_slice(&x);
    uncompressed.extend_from_slice(&y);
    let public = PublicKey::from_sec1_bytes(&uncompressed).map_err(|_| ())?;
    public
        .to_public_key_pem(p256::pkcs8::LineEnding::LF)
        .map_err(|_| ())
}
