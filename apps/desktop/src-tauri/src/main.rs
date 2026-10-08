#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

use serde::{Deserialize, Serialize};
use serde_json::Value;

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct Request {
    api_version: u32,
    request_id: String,
    method: Method,
    params: Value,
}

#[derive(Deserialize, Serialize)]
#[serde(rename_all = "snake_case")]
enum Method {
    GetSnapshot,
    Connect,
    Disconnect,
    UpdateSettings,
}

#[tauri::command]
async fn daemon_request(request: Request) -> Result<Value, String> {
    if request.api_version != 1 || request.request_id.is_empty() || request.request_id.len() > 80 {
        return Err("INVALID_REQUEST".into());
    }
    #[cfg(windows)]
    {
        tokio::time::timeout(std::time::Duration::from_secs(3), exchange(request))
            .await
            .map_err(|_| "DAEMON_TIMEOUT".to_string())?
    }
    #[cfg(not(windows))]
    {
        let _ = request;
        Err("PLATFORM_NOT_SUPPORTED".into())
    }
}

#[cfg(windows)]
async fn exchange(request: Request) -> Result<Value, String> {
    use tokio::io::{AsyncBufReadExt, AsyncReadExt, AsyncWriteExt, BufReader};
    use tokio::net::windows::named_pipe::ClientOptions;
    let pipe = loop {
        match ClientOptions::new().open(r"\\.\pipe\nimbus-vpn-dev-v1") {
            Ok(pipe) => break pipe,
            Err(error) if error.raw_os_error() == Some(231) => {
                tokio::time::sleep(std::time::Duration::from_millis(40)).await;
            }
            Err(_) => return Err("DAEMON_UNAVAILABLE".into()),
        }
    };
    let mut payload = serde_json::to_vec(&request).map_err(|_| "INVALID_REQUEST")?;
    if payload.len() > 8192 {
        return Err("INVALID_REQUEST".into());
    }
    payload.push(b'\n');
    let (read, mut write) = tokio::io::split(pipe);
    write
        .write_all(&payload)
        .await
        .map_err(|_| "DAEMON_IO_ERROR")?;
    let mut reader = BufReader::new(read.take(65537));
    let mut response = Vec::new();
    reader
        .read_until(b'\n', &mut response)
        .await
        .map_err(|_| "DAEMON_IO_ERROR")?;
    if response.len() > 65536 || response.last() != Some(&b'\n') {
        return Err("INVALID_DAEMON_RESPONSE".into());
    }
    let value: Value = serde_json::from_slice(&response).map_err(|_| "INVALID_DAEMON_RESPONSE")?;
    if value["api_version"] != 1 || value["request_id"] != request.request_id {
        return Err("INVALID_DAEMON_RESPONSE".into());
    }
    Ok(value)
}

fn main() {
    tauri::Builder::default()
        .invoke_handler(tauri::generate_handler![daemon_request])
        .run(tauri::generate_context!())
        .expect("failed to start Nimbus desktop");
}
