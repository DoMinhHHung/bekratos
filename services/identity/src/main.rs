use std::net::SocketAddr;
use std::sync::Arc;
use std::time::Duration;

use identity::infra::db;
use identity::infra::mailer::{ConsoleMailer, Mailer, NoopMailer};
use identity::infra::password::PasswordPolicy;
use identity::infra::redis as redis_infra;
use identity::infra::tokens::{parse_hmac_keys, MagicLinkSigner};
use identity::jobs::email_worker::{EmailWorker, EmailWorkerConfig};
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
        _ => Arc::new(NoopMailer),
    };

    let (shutdown_tx, shutdown_rx) = watch::channel(false);

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

    tokio::spawn(async move {
        worker.run(shutdown_rx).await;
    });

    let addr = config.bind_addr;
    let state = AppState::new(config, pool, redis, password_policy, signer);
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
}

async fn shutdown_signal() {
    let ctrl_c = async {
        tokio::signal::ctrl_c()
            .await
            .expect("failed to install Ctrl+C handler");
    };

    #[cfg(unix)]
    let terminate = async {
        tokio::signal::unix::signal(tokio::signal::unix::SignalKind::terminate())
            .expect("failed to install signal handler")
            .recv()
            .await;
    };

    #[cfg(not(unix))]
    let terminate = std::future::pending::<()>();

    tokio::select! {
        _ = ctrl_c => {},
        _ = terminate => {},
    }

    tracing::info!("shutdown signal received");
}
