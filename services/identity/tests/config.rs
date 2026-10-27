use std::env;
use std::process::Command;
use std::time::Duration;

use identity::{AppError, Config};
use uuid::Uuid;

const REQUIRED_ENV: &[(&str, &str)] = &[
    ("DATABASE_URL", "postgres://localhost/identity_config_test"),
    ("REDIS_URL", "redis://localhost:6379"),
    ("MAGIC_LINK_HMAC_KEYS", "test-key:0123456789abcdef"),
    ("MAGIC_LINK_ACTIVE_KID", "test-key"),
    ("APP_PUBLIC_BASE_URL", "https://identity.example.test"),
    ("JWT_ISSUER", "https://identity.example.test"),
    ("JWT_AUDIENCE", "identity-api"),
    (
        "JWT_KEY_ENCRYPTION_KEY",
        "synthetic-config-test-encryption-key",
    ),
];

// Each child runs exactly one test, without inherited variables or a developer's .env.
fn isolated(test_name: &str, check: impl FnOnce()) {
    if env::var("IDENTITY_CONFIG_TEST").as_deref() == Ok(test_name) {
        check();
        return;
    }

    let directory = env::temp_dir().join(format!("identity-config-test-{}", Uuid::now_v7()));
    std::fs::create_dir(&directory).unwrap();
    std::fs::write(directory.join(".env"), "").unwrap();
    let result = Command::new(env::current_exe().unwrap())
        .args(["--exact", test_name, "--nocapture", "--test-threads=1"])
        .env_clear()
        .envs(REQUIRED_ENV.iter().copied())
        .env("IDENTITY_CONFIG_TEST", test_name)
        .current_dir(&directory)
        .output();
    std::fs::remove_dir_all(&directory).unwrap();
    let output = result.expect("start isolated config test");
    assert!(
        output.status.success(),
        "isolated test {test_name} failed:\n{}\n{}",
        String::from_utf8_lossy(&output.stdout),
        String::from_utf8_lossy(&output.stderr)
    );
}

fn with_value(key: &str, value: Option<&str>, check: impl FnOnce()) {
    let previous = env::var_os(key);
    match value {
        Some(value) => env::set_var(key, value),
        None => env::remove_var(key),
    }
    check();
    match previous {
        Some(value) => env::set_var(key, value),
        None => env::remove_var(key),
    }
}

fn assert_config_error(expected_message: &str) {
    match Config::from_env() {
        Err(AppError::Config(message)) => assert!(
            message.contains(expected_message),
            "expected {expected_message:?}, got {message:?}"
        ),
        other => panic!("expected configuration error, got {other:?}"),
    }
}

#[test]
fn required_settings_load_with_documented_defaults_without_connecting_to_services() {
    isolated(
        "required_settings_load_with_documented_defaults_without_connecting_to_services",
        || {
            let config = Config::from_env().unwrap();
            assert_eq!(config.host, "0.0.0.0");
            assert_eq!(config.port, 8080);
            assert_eq!(config.bind_addr, "0.0.0.0:8080".parse().unwrap());
            assert_eq!(config.database_url, REQUIRED_ENV[0].1);
            assert_eq!(config.redis_url, REQUIRED_ENV[1].1);
            assert_eq!(config.magic_link_hmac_keys, REQUIRED_ENV[2].1);
            assert_eq!(config.magic_link_active_kid, "test-key");
            assert_eq!(config.app_public_base_url, "https://identity.example.test");
            assert_eq!(config.jwt_issuer, "https://identity.example.test");
            assert_eq!(config.jwt_audience, "identity-api");
            assert_eq!(config.jwt_key_encryption_key, REQUIRED_ENV[7].1);
            assert_eq!(config.request_timeout, Duration::from_secs(30));
            assert_eq!(config.body_limit_bytes, 1_048_576);
            assert!(config.readiness_require_db);
            assert!(config.readiness_require_redis);
            assert_eq!(config.mail_provider, "console");
            assert_eq!(config.mail_from, "noreply@localhost");
            assert_eq!(config.mail_api_key, "");
            assert_eq!(config.mail_api_base_url, "https://api.resend.com");
            assert_eq!(config.mail_timeout_secs, 10);
            assert!(config.identity_service_api_token.is_none());
            assert_eq!(config.argon2_memory_kib, 19_456);
            assert_eq!(config.argon2_iterations, 2);
            assert_eq!(config.argon2_parallelism, 1);
            assert_eq!(config.argon2_max_concurrent, 2);
            assert!(config.jwt_signing_key_pem.is_empty());
            assert_eq!(config.jwt_kid, "es256-1");
            assert_eq!(config.jwt_access_ttl_secs, 900);
            assert_eq!(config.refresh_ttl_secs, 2_592_000);
            assert_eq!(config.session_ttl_secs, 2_592_000);
            assert_eq!(config.jwks_cache_ttl_secs, 60);
        },
    );
}

#[test]
fn explicit_settings_override_every_optional_default() {
    isolated("explicit_settings_override_every_optional_default", || {
        for (key, value) in [
            ("HOST", "127.0.0.1"),
            ("PORT", "9090"),
            ("REQUEST_TIMEOUT_SECS", "5"),
            ("BODY_LIMIT_BYTES", "2048"),
            ("READINESS_REQUIRE_DB", "false"),
            ("READINESS_REQUIRE_REDIS", "off"),
            ("MAIL_PROVIDER", "none"),
            ("MAIL_FROM", "test@example.test"),
            ("MAIL_API_KEY", "synthetic-resend-test-api-key"),
            ("MAIL_API_BASE_URL", "https://api.resend.com"),
            ("MAIL_TIMEOUT_SECS", "20"),
            ("ARGON2_MEMORY_KIB", "32768"),
            ("ARGON2_ITERATIONS", "3"),
            ("ARGON2_PARALLELISM", "2"),
            ("ARGON2_MAX_CONCURRENT", "4"),
            ("JWT_SIGNING_KEY_PEM", "synthetic-fallback-pem"),
            ("JWT_KID", "rotated-key"),
            ("JWT_ACCESS_TTL_SECS", "60"),
            ("REFRESH_TTL_SECS", "3600"),
            ("SESSION_TTL_SECS", "7200"),
            ("JWKS_CACHE_TTL_SECS", "10"),
        ] {
            env::set_var(key, value);
        }
        let config = Config::from_env().unwrap();
        assert_eq!(config.host, "127.0.0.1");
        assert_eq!(config.port, 9090);
        assert_eq!(config.bind_addr, "127.0.0.1:9090".parse().unwrap());
        assert_eq!(config.request_timeout, Duration::from_secs(5));
        assert_eq!(config.body_limit_bytes, 2048);
        assert!(!config.readiness_require_db);
        assert!(!config.readiness_require_redis);
        assert_eq!(config.mail_provider, "none");
        assert_eq!(config.mail_from, "test@example.test");
        assert_eq!(config.mail_api_key, "synthetic-resend-test-api-key");
        assert_eq!(config.mail_api_base_url, "https://api.resend.com");
        assert_eq!(config.mail_timeout_secs, 20);
        assert_eq!(config.argon2_memory_kib, 32_768);
        assert_eq!(config.argon2_iterations, 3);
        assert_eq!(config.argon2_parallelism, 2);
        assert_eq!(config.argon2_max_concurrent, 4);
        assert_eq!(config.jwt_signing_key_pem, "synthetic-fallback-pem");
        assert_eq!(config.jwt_kid, "rotated-key");
        assert_eq!(config.jwt_access_ttl_secs, 60);
        assert_eq!(config.refresh_ttl_secs, 3600);
        assert_eq!(config.session_ttl_secs, 7200);
        assert_eq!(config.jwks_cache_ttl_secs, 10);
    });
}

#[test]
fn dotenv_supplies_missing_settings_and_preserves_environment_overrides() {
    isolated(
        "dotenv_supplies_missing_settings_and_preserves_environment_overrides",
        || {
            env::remove_var("REDIS_URL");
            std::fs::write(
            ".env",
            "DATABASE_URL=postgres://dotenv.invalid/unused\nREDIS_URL=redis://dotenv.invalid:6379\nPORT=8181\n",
        )
        .unwrap();
            env::set_var("PORT", "9191");
            let config = Config::from_env().unwrap();
            assert_eq!(config.database_url, REQUIRED_ENV[0].1);
            assert_eq!(config.redis_url, "redis://dotenv.invalid:6379");
            assert_eq!(config.port, 9191);
        },
    );
}

#[test]
fn bind_address_accepts_ipv4_and_both_ipv6_forms_at_port_boundaries() {
    isolated(
        "bind_address_accepts_ipv4_and_both_ipv6_forms_at_port_boundaries",
        || {
            for (host, address) in [
                ("127.0.0.1", "127.0.0.1"),
                ("::1", "::1"),
                ("[::1]", "::1"),
                ("2001:db8::1", "2001:db8::1"),
                ("[2001:db8::1]", "2001:db8::1"),
            ] {
                env::set_var("HOST", host);
                for port in [0, u16::MAX] {
                    env::set_var("PORT", port.to_string());
                    let config = Config::from_env().unwrap();
                    assert_eq!(
                        config.bind_addr.ip(),
                        address.parse::<std::net::IpAddr>().unwrap()
                    );
                    assert_eq!(config.bind_addr.port(), port);
                }
            }
        },
    );
}

#[test]
fn bind_address_rejects_hostnames_malformed_ips_and_embedded_ports() {
    isolated(
        "bind_address_rejects_hostnames_malformed_ips_and_embedded_ports",
        || {
            for host in [
                "",
                "localhost",
                "256.0.0.1",
                "[::1",
                "127.0.0.1:8080",
                "[::1]:8080",
            ] {
                with_value("HOST", Some(host), || assert_config_error("HOST:PORT"));
            }
        },
    );
}

#[test]
fn each_required_setting_must_be_present() {
    isolated("each_required_setting_must_be_present", || {
        for (key, _) in REQUIRED_ENV {
            with_value(key, None, || {
                assert_config_error(&format!("{key} is required"))
            });
        }
    });
}

#[test]
fn connection_urls_and_public_base_url_must_not_be_empty() {
    isolated(
        "connection_urls_and_public_base_url_must_not_be_empty",
        || {
            for key in ["DATABASE_URL", "REDIS_URL", "APP_PUBLIC_BASE_URL"] {
                with_value(key, Some(""), || {
                    assert_config_error(&format!("{key} must not be empty"))
                });
            }
        },
    );
}

#[test]
fn numeric_settings_reject_malformed_and_overflowing_values() {
    isolated(
        "numeric_settings_reject_malformed_and_overflowing_values",
        || {
            for key in [
                "PORT",
                "REQUEST_TIMEOUT_SECS",
                "BODY_LIMIT_BYTES",
                "ARGON2_MEMORY_KIB",
                "ARGON2_ITERATIONS",
                "ARGON2_PARALLELISM",
                "ARGON2_MAX_CONCURRENT",
                "JWT_ACCESS_TTL_SECS",
                "REFRESH_TTL_SECS",
                "SESSION_TTL_SECS",
                "JWKS_CACHE_TTL_SECS",
            ] {
                for value in [
                    "",
                    "not-a-number",
                    "1.5",
                    " 1",
                    "1 ",
                    "18446744073709551616",
                ] {
                    with_value(key, Some(value), || assert_config_error(key));
                }
            }
            with_value("PORT", Some("65536"), || assert_config_error("PORT"));
            for key in [
                "ARGON2_MEMORY_KIB",
                "ARGON2_ITERATIONS",
                "ARGON2_PARALLELISM",
            ] {
                with_value(key, Some("4294967296"), || assert_config_error(key));
            }
            for key in [
                "JWT_ACCESS_TTL_SECS",
                "REFRESH_TTL_SECS",
                "SESSION_TTL_SECS",
            ] {
                with_value(key, Some("9223372036854775808"), || {
                    assert_config_error(key)
                });
            }
        },
    );
}

#[test]
fn timeouts_body_limit_and_password_concurrency_must_be_positive() {
    isolated(
        "timeouts_body_limit_and_password_concurrency_must_be_positive",
        || {
            for key in [
                "REQUEST_TIMEOUT_SECS",
                "BODY_LIMIT_BYTES",
                "ARGON2_MAX_CONCURRENT",
            ] {
                for value in ["0", "-1"] {
                    with_value(key, Some(value), || assert_config_error(key));
                }
                with_value(key, Some("1"), || {
                    Config::from_env().unwrap();
                });
            }
        },
    );
}

#[test]
fn every_token_and_session_ttl_must_be_positive() {
    isolated("every_token_and_session_ttl_must_be_positive", || {
        for key in [
            "JWT_ACCESS_TTL_SECS",
            "REFRESH_TTL_SECS",
            "SESSION_TTL_SECS",
        ] {
            for value in ["0", "-1", "-9223372036854775808"] {
                with_value(key, Some(value), || {
                    assert_config_error("token/session TTLs must be > 0")
                });
            }
            with_value(key, Some("1"), || {
                Config::from_env().unwrap();
            });
        }
    });
}

#[test]
fn readiness_flags_accept_case_insensitive_boolean_aliases() {
    isolated(
        "readiness_flags_accept_case_insensitive_boolean_aliases",
        || {
            for key in ["READINESS_REQUIRE_DB", "READINESS_REQUIRE_REDIS"] {
                for (value, expected) in [
                    ("1", true),
                    ("true", true),
                    ("TRUE", true),
                    ("YeS", true),
                    ("ON", true),
                    ("0", false),
                    ("false", false),
                    ("FALSE", false),
                    ("nO", false),
                    ("Off", false),
                ] {
                    with_value(key, Some(value), || {
                        let config = Config::from_env().unwrap();
                        if key == "READINESS_REQUIRE_DB" {
                            assert_eq!(config.readiness_require_db, expected);
                            assert!(config.readiness_require_redis);
                        } else {
                            assert_eq!(config.readiness_require_redis, expected);
                            assert!(config.readiness_require_db);
                        }
                    });
                }
            }
        },
    );
}

#[test]
fn readiness_flags_reject_unknown_empty_and_padded_values() {
    isolated(
        "readiness_flags_reject_unknown_empty_and_padded_values",
        || {
            for key in ["READINESS_REQUIRE_DB", "READINESS_REQUIRE_REDIS"] {
                for value in ["", "2", "enabled", " true", "false "] {
                    with_value(key, Some(value), || assert_config_error(key));
                }
            }
        },
    );
}

#[test]
fn placeholder_keys_and_short_encryption_keys_are_rejected() {
    isolated(
        "placeholder_keys_and_short_encryption_keys_are_rejected",
        || {
            with_value("MAGIC_LINK_HMAC_KEYS", Some("test:change-me"), || {
                assert_config_error("MAGIC_LINK_HMAC_KEYS must not use a placeholder");
            });
            for length in [0, 1, 31] {
                with_value("JWT_KEY_ENCRYPTION_KEY", Some(&"x".repeat(length)), || {
                    assert_config_error("JWT_KEY_ENCRYPTION_KEY must be at least 32");
                });
            }
            with_value(
                "JWT_KEY_ENCRYPTION_KEY",
                Some(&"change-me".repeat(4)),
                || {
                    assert_config_error("JWT_KEY_ENCRYPTION_KEY must not use a placeholder");
                },
            );
            with_value("JWT_KEY_ENCRYPTION_KEY", Some(&"x".repeat(32)), || {
                assert_eq!(Config::from_env().unwrap().jwt_key_encryption_key.len(), 32);
            });
        },
    );
}

#[test]
fn zero_jwks_cache_ttl_is_accepted() {
    isolated("zero_jwks_cache_ttl_is_accepted", || {
        env::set_var("JWKS_CACHE_TTL_SECS", "0");
        assert_eq!(Config::from_env().unwrap().jwks_cache_ttl_secs, 0);
    });
}

#[test]
fn debug_output_redacts_all_connection_and_key_material() {
    isolated(
        "debug_output_redacts_all_connection_and_key_material",
        || {
            for (key, value) in [
                ("DATABASE_URL", "synthetic-database-credential"),
                ("REDIS_URL", "synthetic-redis-credential"),
                ("MAGIC_LINK_HMAC_KEYS", "synthetic-hmac-material"),
                ("JWT_SIGNING_KEY_PEM", "synthetic-private-signing-material"),
                (
                    "JWT_KEY_ENCRYPTION_KEY",
                    "synthetic-encryption-material-for-debug-test",
                ),
            ] {
                env::set_var(key, value);
            }
            let config = Config::from_env().unwrap();
            for debug in [format!("{config:?}"), format!("{config:#?}")] {
                for (field, secret) in [
                    ("database_url", &config.database_url),
                    ("redis_url", &config.redis_url),
                    ("magic_link_hmac_keys", &config.magic_link_hmac_keys),
                    ("jwt_signing_key_pem", &config.jwt_signing_key_pem),
                    ("jwt_key_encryption_key", &config.jwt_key_encryption_key),
                ] {
                    assert!(!debug.contains(secret), "debug exposed {field}");
                    assert!(debug.contains(&format!("{field}: \"[REDACTED]\"")));
                }
                assert!(debug.contains(&config.jwt_issuer));
                assert!(debug.contains(&config.jwt_audience));
            }
        },
    );
}
