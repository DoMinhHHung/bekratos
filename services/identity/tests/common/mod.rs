use std::env;
use std::path::PathBuf;

pub fn load_test_env() {
    let candidates = [
        PathBuf::from(".env"),
        PathBuf::from("services/identity/.env"),
        PathBuf::from("../.env"),
    ];

    for path in candidates {
        if path.exists() {
            let _ = dotenvy::from_path(&path);
            break;
        }
    }
}

pub fn test_database_url() -> Option<String> {
    load_test_env();

    env::var("DATABASE_URL_TEST").ok().filter(|u| !u.is_empty())
}
