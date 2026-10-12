#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

use serde::{Deserialize, Serialize};
use serde_json::Value;
use tauri::Manager;

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
    Login,
    Logout,
    RefreshAccount,
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
    use std::os::windows::io::AsRawHandle;
    use tokio::io::{AsyncBufReadExt, AsyncReadExt, AsyncWriteExt, BufReader};
    use tokio::net::windows::named_pipe::ClientOptions;
    let real = std::env::var("LIGHTFLOW_REAL_DESKTOP").as_deref() == Ok("1");
    let name = if real {
        r"\\.\pipe\lightflow-desktop-real-v1"
    } else {
        r"\\.\pipe\nimbus-vpn-dev-v1"
    };
    let pipe = loop {
        match ClientOptions::new().open(name) {
            Ok(pipe) => break pipe,
            Err(error) if error.raw_os_error() == Some(231) => {
                tokio::time::sleep(std::time::Duration::from_millis(40)).await;
            }
            Err(_) => return Err("DAEMON_UNAVAILABLE".into()),
        }
    };
    if real && !system_pipe_server(pipe.as_raw_handle()) {
        return Err("UNTRUSTED_DAEMON".into());
    }
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

#[cfg(windows)]
fn system_pipe_server(pipe: std::os::windows::io::RawHandle) -> bool {
    use std::ffi::c_void;
    #[link(name = "kernel32")]
    extern "system" {
        fn GetNamedPipeServerProcessId(pipe: *mut c_void, pid: *mut u32) -> i32;
    }
    #[link(name = "advapi32")]
    extern "system" {
        fn OpenSCManagerW(machine: *const u16, database: *const u16, access: u32) -> *mut c_void;
        fn OpenServiceW(manager: *mut c_void, name: *const u16, access: u32) -> *mut c_void;
        fn QueryServiceStatusEx(
            service: *mut c_void,
            level: u32,
            data: *mut u8,
            size: u32,
            needed: *mut u32,
        ) -> i32;
        fn CloseServiceHandle(handle: *mut c_void) -> i32;
    }
    unsafe {
        let mut pid = 0;
        if GetNamedPipeServerProcessId(pipe, &mut pid) == 0 {
            return false;
        }
        let manager = OpenSCManagerW(std::ptr::null(), std::ptr::null(), 1);
        if manager.is_null() {
            return false;
        }
        let name: Vec<u16> = "LightflowDesktopDev\0".encode_utf16().collect();
        let service = OpenServiceW(manager, name.as_ptr(), 4);
        CloseServiceHandle(manager);
        if service.is_null() {
            return false;
        }
        let mut data = [0u32; 9];
        let mut needed = 0;
        let ok = QueryServiceStatusEx(
            service,
            0,
            data.as_mut_ptr().cast(),
            std::mem::size_of_val(&data) as u32,
            &mut needed,
        ) != 0;
        CloseServiceHandle(service);
        ok && data[1] == 4 && data[7] == pid
    }
}

fn main() {
    tauri::Builder::default()
        .invoke_handler(tauri::generate_handler![daemon_request])
        .on_window_event(|window, event| {
            if std::env::var("LIGHTFLOW_REAL_DESKTOP").as_deref() != Ok("1") {
                return;
            }
            if let tauri::WindowEvent::CloseRequested { api, .. } = event {
                api.prevent_close();
                let app = window.app_handle().clone();
                tauri::async_runtime::spawn(async move {
                    let id = std::time::SystemTime::now()
                        .duration_since(std::time::UNIX_EPOCH)
                        .unwrap_or_default()
                        .as_nanos();
                    let disconnect = Request {
                        api_version: 1,
                        request_id: format!("window-close-{id}"),
                        method: Method::Disconnect,
                        params: serde_json::json!({}),
                    };
                    if daemon_request(disconnect).await.is_err() {
                        return;
                    }
                    for attempt in 0..90 {
                        tokio::time::sleep(std::time::Duration::from_millis(500)).await;
                        let status = Request {
                            api_version: 1,
                            request_id: format!("window-close-{attempt}"),
                            method: Method::GetSnapshot,
                            params: serde_json::json!({}),
                        };
                        if let Ok(value) = daemon_request(status).await {
                            if value["result"]["state"] == "disconnected"
                                && value["result"].get("last_error").is_none()
                            {
                                app.exit(0);
                                return;
                            }
                        }
                    }
                });
            }
        })
        .run(tauri::generate_context!())
        .expect("failed to start Nimbus desktop");
}

#[cfg(all(test, windows))]
mod tests {
    use super::*;

    #[test]
    #[ignore = "requires the installed development Windows service"]
    fn installed_service_identity_and_snapshot() {
        std::env::set_var("LIGHTFLOW_REAL_DESKTOP", "1");
        let runtime = tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .unwrap();
        let response = runtime
            .block_on(exchange(Request {
                api_version: 1,
                request_id: "rust-live-service-check".into(),
                method: Method::GetSnapshot,
                params: serde_json::json!({}),
            }))
            .expect("real service identity or IPC failed");
        assert_eq!(response["result"]["simulation"], false);
        assert!(response["result"].get("access_token").is_none());
        assert!(response["result"].get("device_key").is_none());
        std::env::remove_var("LIGHTFLOW_REAL_DESKTOP");
    }
}
