use std::sync::Arc;
use std::time::Duration;

use async_trait::async_trait;
use base64::engine::general_purpose::STANDARD;
use base64::Engine;
use rustls::pki_types::ServerName;
use rustls::{ClientConfig, RootCertStore};
use tokio::io::{AsyncRead, AsyncReadExt, AsyncWrite, AsyncWriteExt, BufReader};
use tokio::net::TcpStream;
use tokio::time::timeout;
use tokio_rustls::TlsConnector;
use webpki_roots::TLS_SERVER_ROOTS;

use crate::error::{AppError, AppResult};
use crate::infra::mailer::{Mailer, OutboundEmail};

const MAX_RESPONSE_BYTES: usize = 16 * 1024;
const MAX_RESPONSE_LINE_BYTES: usize = 4096;
const MAX_RESPONSE_LINES: usize = 32;
const MAX_MESSAGE_BYTES: usize = 128 * 1024;

pub struct SmtpMailer {
    host: String,
    port: u16,
    username: String,
    app_password: String,
    from: String,
    timeout: Duration,
    tls_config: Arc<ClientConfig>,
}

impl SmtpMailer {
    pub fn new(
        host: String,
        port: u16,
        username: String,
        app_password: String,
        from: String,
        timeout: Duration,
    ) -> Self {
        let mut roots = RootCertStore::empty();
        roots.extend(TLS_SERVER_ROOTS.iter().cloned());
        let tls_config = ClientConfig::builder()
            .with_root_certificates(roots)
            .with_no_client_auth();

        Self {
            host,
            port,
            username,
            app_password,
            from,
            timeout,
            tls_config: Arc::new(tls_config),
        }
    }

    async fn send_inner(&self, email: OutboundEmail) -> AppResult<()> {
        let tcp = TcpStream::connect((self.host.as_str(), self.port))
            .await
            .map_err(|_| AppError::Unavailable)?;
        let server_name =
            ServerName::try_from(self.host.clone()).map_err(|_| AppError::Unavailable)?;
        let tls = TlsConnector::from(self.tls_config.clone())
            .connect(server_name, tcp)
            .await
            .map_err(|_| AppError::Unavailable)?;

        smtp_exchange(
            tls,
            &self.from,
            &email.to,
            &email.subject,
            &email.text_body,
            &self.username,
            &self.app_password,
        )
        .await
    }
}

#[async_trait]
impl Mailer for SmtpMailer {
    async fn send(&self, email: OutboundEmail) -> AppResult<()> {
        if self.host.is_empty()
            || self.username.trim().is_empty()
            || self.app_password.trim().is_empty()
            || self.from.trim().is_empty()
        {
            return Err(AppError::Unavailable);
        }
        timeout(self.timeout, self.send_inner(email))
            .await
            .map_err(|_| AppError::Unavailable)?
    }
}

async fn smtp_exchange<S>(
    stream: S,
    from: &str,
    to: &str,
    subject: &str,
    body: &str,
    username: &str,
    app_password: &str,
) -> AppResult<()>
where
    S: AsyncRead + AsyncWrite + Unpin,
{
    validate_mailbox(from)?;
    validate_mailbox(to)?;
    if body.len() > MAX_MESSAGE_BYTES
        || subject.is_empty()
        || subject.len() > 998
        || (subject.contains('\r') || subject.contains('\n'))
        || !subject.is_ascii()
    {
        return Err(AppError::Unavailable);
    }

    let mut stream = BufReader::new(stream);
    expect_response(&mut stream, &[220]).await?;
    command(&mut stream, "EHLO bekratos.invalid", &[250]).await?;
    command(&mut stream, "AUTH LOGIN", &[334]).await?;
    command(&mut stream, STANDARD.encode(username.as_bytes()).as_str(), &[334]).await?;
    command(
        &mut stream,
        STANDARD.encode(app_password.as_bytes()).as_str(),
        &[235],
    )
    .await?;
    command(
        &mut stream,
        format!("MAIL FROM:<{from}>").as_str(),
        &[250],
    )
    .await?;
    command(
        &mut stream,
        format!("RCPT TO:<{to}>").as_str(),
        &[250, 251],
    )
    .await?;
    command(&mut stream, "DATA", &[354]).await?;

    let encoded_body = encode_body(body);
    let message = format!(
        "From: <{from}>\r\nTo: <{to}>\r\nSubject: {subject}\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: base64\r\n\r\n{encoded_body}\r\n"
    );
    if message.len() > MAX_MESSAGE_BYTES {
        return Err(AppError::Unavailable);
    }
    stream
        .get_mut()
        .write_all(message.as_bytes())
        .await
        .map_err(|_| AppError::Unavailable)?;
    stream
        .get_mut()
        .write_all(b".\r\n")
        .await
        .map_err(|_| AppError::Unavailable)?;
    stream
        .get_mut()
        .flush()
        .await
        .map_err(|_| AppError::Unavailable)?;
    expect_response(&mut stream, &[250]).await?;
    let _ = command(&mut stream, "QUIT", &[221]).await;
    Ok(())
}

async fn command<S>(
    stream: &mut BufReader<S>,
    command: &str,
    accepted: &[u16],
) -> AppResult<()>
where
    S: AsyncRead + AsyncWrite + Unpin,
{
    if (command.contains('\r') || command.contains('\n')) || command.len() > MAX_RESPONSE_LINE_BYTES {
        return Err(AppError::Unavailable);
    }
    stream
        .get_mut()
        .write_all(command.as_bytes())
        .await
        .map_err(|_| AppError::Unavailable)?;
    stream
        .get_mut()
        .write_all(b"\r\n")
        .await
        .map_err(|_| AppError::Unavailable)?;
    stream
        .get_mut()
        .flush()
        .await
        .map_err(|_| AppError::Unavailable)?;
    expect_response(stream, accepted).await
}

async fn expect_response<S>(stream: &mut BufReader<S>, accepted: &[u16]) -> AppResult<()>
where
    S: AsyncRead + Unpin,
{
    let mut total = 0usize;
    let mut multiline_code = None;
    for _ in 0..MAX_RESPONSE_LINES {
        let mut line = Vec::with_capacity(128);
        loop {
            let byte = stream.read_u8().await.map_err(|_| AppError::Unavailable)?;
            line.push(byte);
            total = total.saturating_add(1);
            if line.len() > MAX_RESPONSE_LINE_BYTES || total > MAX_RESPONSE_BYTES {
                return Err(AppError::Unavailable);
            }
            if byte == b'\n' {
                break;
            }
        }
        if line.len() < 5
            || !line.ends_with(b"\r\n")
            || !line[..3].iter().all(u8::is_ascii_digit)
            || !matches!(line[3], b'-' | b' ')
        {
            return Err(AppError::Unavailable);
        }
        let code = u16::from(line[0] - b'0') * 100
            + u16::from(line[1] - b'0') * 10
            + u16::from(line[2] - b'0');
        if let Some(expected) = multiline_code {
            if code != expected {
                return Err(AppError::Unavailable);
            }
        }
        if line[3] == b'-' {
            multiline_code = Some(code);
            continue;
        }
        if accepted.contains(&code) {
            return Ok(());
        }
        return Err(AppError::Unavailable);
    }
    Err(AppError::Unavailable)
}

fn validate_mailbox(value: &str) -> AppResult<()> {
    let mut parts = value.split('@');
    let local = parts.next().unwrap_or_default();
    let domain = parts.next().unwrap_or_default();
    if local.is_empty()
        || domain.is_empty()
        || parts.next().is_some()
        || value.len() > 254
        || value.bytes().any(|b| {
            b.is_ascii_control()
                || b.is_ascii_whitespace()
                || matches!(b, b'<' | b'>')
        })
    {
        return Err(AppError::Unavailable);
    }
    Ok(())
}

fn encode_body(body: &str) -> String {
    STANDARD
        .encode(body.as_bytes())
        .as_bytes()
        .chunks(76)
        .map(|line| std::str::from_utf8(line).unwrap_or_default())
        .collect::<Vec<_>>()
        .join("\r\n")
}

#[cfg(test)]
mod tests {
    use super::{encode_body, smtp_exchange, validate_mailbox};
    use tokio::io::{AsyncBufReadExt, AsyncWriteExt, BufReader};

    async fn read_line(stream: &mut BufReader<tokio::io::DuplexStream>) -> String {
        let mut line = String::new();
        stream.read_line(&mut line).await.unwrap();
        line
    }

    async fn write_response(stream: &mut BufReader<tokio::io::DuplexStream>, response: &str) {
        stream.get_mut().write_all(response.as_bytes()).await.unwrap();
        stream.get_mut().flush().await.unwrap();
    }

    async fn fake_smtp(stream: tokio::io::DuplexStream, auth_success: bool, accept_message: bool) {
        let mut stream = BufReader::new(stream);
        write_response(&mut stream, "220 smtp.test ESMTP\r\n").await;
        assert!(read_line(&mut stream).await.starts_with("EHLO "));
        write_response(&mut stream, "250-smtp.test\r\n250 AUTH LOGIN\r\n").await;
        assert_eq!(read_line(&mut stream).await, "AUTH LOGIN\r\n");
        write_response(&mut stream, "334 VXNlcm5hbWU6\r\n").await;
        assert_eq!(read_line(&mut stream).await, "dXNlckBleGFtcGxlLmNvbQ==\r\n");
        if !auth_success {
            write_response(&mut stream, "535 5.7.8 authentication failed\r\n").await;
            return;
        }
        write_response(&mut stream, "334 UGFzc3dvcmQ6\r\n").await;
        assert_eq!(read_line(&mut stream).await, "c2VjcmV0LWFwcC1wYXNzd29yZA==\r\n");
        write_response(&mut stream, "235 2.7.0 authenticated\r\n").await;
        assert!(read_line(&mut stream).await.starts_with("MAIL FROM:"));
        write_response(&mut stream, "250 2.1.0 sender ok\r\n").await;
        assert!(read_line(&mut stream).await.starts_with("RCPT TO:"));
        write_response(&mut stream, "250 2.1.5 recipient ok\r\n").await;
        assert_eq!(read_line(&mut stream).await, "DATA\r\n");
        write_response(&mut stream, "354 send message\r\n").await;
        loop {
            let line = read_line(&mut stream).await;
            if line == ".\r\n" {
                break;
            }
        }
        if accept_message {
            write_response(&mut stream, "250 2.0.0 accepted\r\n").await;
            assert_eq!(read_line(&mut stream).await, "QUIT\r\n");
            write_response(&mut stream, "221 2.0.0 goodbye\r\n").await;
        } else {
            write_response(&mut stream, "451 4.3.0 temporary failure\r\n").await;
        }
    }

    #[tokio::test]
    async fn accepts_successful_smtp_delivery() {
        let (client, server) = tokio::io::duplex(64 * 1024);
        let server_task = tokio::spawn(fake_smtp(server, true, true));
        let result = smtp_exchange(
            client,
            "sender@example.com",
            "user@example.com",
            "Verify your email",
            "magic link https://example.com/verify?token=abc",
            "user@example.com",
            "secret-app-password",
        )
        .await;
        assert!(result.is_ok());
        server_task.await.unwrap();
    }

    #[tokio::test]
    async fn rejects_smtp_authentication_failure() {
        let (client, server) = tokio::io::duplex(64 * 1024);
        let server_task = tokio::spawn(fake_smtp(server, false, false));
        let result = smtp_exchange(
            client,
            "sender@example.com",
            "user@example.com",
            "Verify your email",
            "body",
            "user@example.com",
            "secret-app-password",
        )
        .await;
        assert!(result.is_err());
        server_task.await.unwrap();
    }

    #[tokio::test]
    async fn retries_temporary_smtp_rejection() {
        let (client, server) = tokio::io::duplex(64 * 1024);
        let server_task = tokio::spawn(fake_smtp(server, true, false));
        let result = smtp_exchange(
            client,
            "sender@example.com",
            "user@example.com",
            "Verify your email",
            "body",
            "user@example.com",
            "secret-app-password",
        )
        .await;
        assert!(result.is_err());
        server_task.await.unwrap();
    }

    #[tokio::test]
    async fn rejects_malformed_and_oversized_smtp_responses() {
        let responses = vec![
            "not an SMTP response\r\n".to_string(),
            format!("220 {}\r\n", "x".repeat(5000)),
        ];
        for response in responses {
            let (client, mut server) = tokio::io::duplex(16 * 1024);
            let server_task = tokio::spawn(async move {
                server.write_all(response.as_bytes()).await.unwrap();
                server.flush().await.unwrap();
            });
            let result = smtp_exchange(
                client,
                "sender@example.com",
                "user@example.com",
                "Verify your email",
                "body",
                "user@example.com",
                "secret-app-password",
            )
            .await;
            assert!(result.is_err());
            server_task.await.unwrap();
        }
    }

    #[tokio::test]
    async fn bounds_smtp_response_wait_time() {
        let (client, _server) = tokio::io::duplex(1024);
        let result = tokio::time::timeout(
            std::time::Duration::from_millis(20),
            smtp_exchange(
                client,
                "sender@example.com",
                "user@example.com",
                "Verify your email",
                "body",
                "user@example.com",
                "secret-app-password",
            ),
        )
        .await;
        assert!(result.is_err());
    }

    #[test]
    fn validates_mailboxes_without_header_or_command_injection() {
        assert!(validate_mailbox("user@example.com").is_ok());
        assert!(validate_mailbox("user@example.com\r\nRCPT TO:<attacker@example.com>").is_err());
        assert!(validate_mailbox("user@example.com\n").is_err());
        assert!(validate_mailbox("not-an-email").is_err());
    }

    #[test]
    fn encodes_utf8_body_as_bounded_mime_lines() {
        let encoded = encode_body("magic link https://example.com/verify?token=abc");
        assert!(encoded.is_ascii());
        assert!(encoded.lines().all(|line| line.len() <= 76));
    }
}
