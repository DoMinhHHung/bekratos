use std::io::{self, Write};
use std::sync::{Arc, Mutex};

use identity::infra::mailer::{ConsoleMailer, Mailer, NoopMailer, OutboundEmail};
use identity::AppError;
use tracing::instrument::WithSubscriber;

#[derive(Clone)]
struct LogWriter(Arc<Mutex<Vec<u8>>>);

impl Write for LogWriter {
    fn write(&mut self, bytes: &[u8]) -> io::Result<usize> {
        self.0.lock().unwrap().write(bytes)
    }

    fn flush(&mut self) -> io::Result<()> {
        Ok(())
    }
}

fn email() -> OutboundEmail {
    OutboundEmail {
        to: "recipient@example.test".into(),
        subject: "Verify your email".into(),
        text_body: "https://identity.example.test/verify-email?token=synthetic-private-token"
            .into(),
    }
}

#[tokio::test]
async fn console_mailer_accepts_email_without_logging_the_magic_link_or_token() {
    let logs = Arc::new(Mutex::new(Vec::new()));
    let writer = LogWriter(logs.clone());
    let subscriber = tracing_subscriber::fmt()
        .without_time()
        .with_ansi(false)
        .with_max_level(tracing::Level::INFO)
        .with_writer(move || writer.clone())
        .finish();
    let message = email();
    let private_body = message.text_body.clone();
    let mailer: Box<dyn Mailer> = Box::new(ConsoleMailer {
        from: "sender@example.test".into(),
    });

    mailer
        .send(message)
        .with_subscriber(subscriber)
        .await
        .unwrap();

    let output = String::from_utf8(logs.lock().unwrap().clone()).unwrap();
    assert!(output.contains("console mailer: email accepted"));
    assert!(!output.contains("recipient@example.test"));
    assert!(!output.contains(&private_body));
    assert!(!output.contains("https://identity.example.test/verify-email"));
    assert!(!output.contains("synthetic-private-token"));
}

#[tokio::test]
async fn disabled_mailer_reports_unavailable_so_delivery_can_be_retried() {
    let mailer: Box<dyn Mailer> = Box::new(NoopMailer);
    for _ in 0..2 {
        assert!(matches!(
            mailer.send(email()).await,
            Err(AppError::Unavailable)
        ));
    }
}
