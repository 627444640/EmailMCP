use serde_json::Value;
use std::{
    fs,
    time::{SystemTime, UNIX_EPOCH},
};
use tauri::{
    menu::{Menu, MenuItem},
    tray::TrayIconBuilder,
    App, AppHandle, Emitter, Manager,
};
use tauri_plugin_autostart::ManagerExt;
use tauri_plugin_shell::{process::CommandEvent, ShellExt};
#[cfg(windows)]
use windows::Win32::{
    Foundation::{LPARAM, WPARAM},
    UI::WindowsAndMessaging::{CreateIcon, SendMessageW, ICON_BIG, WM_SETICON},
};

#[tauri::command]
async fn load_config(app: AppHandle) -> Result<Value, String> {
    let output = run_sidecar(app, vec!["config".into(), "get".into()], None).await?;
    serde_json::from_str(&output).map_err(|err| err.to_string())
}

#[tauri::command]
async fn save_config(app: AppHandle, config: Value) -> Result<(), String> {
    let path = temp_json_path("email-mcp-config");
    let data = serde_json::to_vec_pretty(&config).map_err(|err| err.to_string())?;
    fs::write(&path, data).map_err(|err| err.to_string())?;

    let result = run_sidecar(
        app,
        vec![
            "config".into(),
            "save".into(),
            "--input".into(),
            path.to_string_lossy().to_string(),
        ],
        None,
    )
    .await
    .map(|_| ());
    let _ = fs::remove_file(path);
    result
}

#[tauri::command]
async fn set_secret(app: AppHandle, kind: String, value: String) -> Result<(), String> {
    run_sidecar(
        app,
        vec!["config".into(), "set-secret".into(), "--kind".into(), kind],
        Some(format!("{value}\n")),
    )
    .await
    .map(|_| ())
}

#[tauri::command]
async fn run_doctor(app: AppHandle) -> Result<Value, String> {
    let output = run_sidecar(app, vec!["doctor".into(), "--json".into()], None).await?;
    serde_json::from_str(&output).map_err(|err| err.to_string())
}

#[tauri::command]
async fn index_status(app: AppHandle) -> Result<Value, String> {
    let output = run_sidecar(app, vec!["index".into(), "status".into()], None).await?;
    serde_json::from_str(&output).map_err(|err| err.to_string())
}

#[tauri::command]
async fn index_sync(
    app: AppHandle,
    limit_per_folder: u32,
    full_bodies: bool,
) -> Result<Value, String> {
    let mut args = vec![
        "index".into(),
        "sync".into(),
        "--limit-per-folder".into(),
        limit_per_folder.to_string(),
    ];
    if full_bodies {
        args.push("--full".into());
    }
    let output = run_sidecar(app, args, None).await?;
    serde_json::from_str(&output).map_err(|err| err.to_string())
}

#[tauri::command]
async fn install_codex(app: AppHandle) -> Result<Value, String> {
    let output = run_sidecar(app, vec!["config".into(), "install-codex".into()], None).await?;
    serde_json::from_str(&output).map_err(|err| err.to_string())
}

#[tauri::command]
async fn restore_codex_backup(app: AppHandle, backup_path: String) -> Result<(), String> {
    run_sidecar(
        app,
        vec![
            "config".into(),
            "restore-codex".into(),
            "--backup".into(),
            backup_path,
        ],
        None,
    )
    .await
    .map(|_| ())
}

#[tauri::command]
fn get_autostart(app: AppHandle) -> Result<bool, String> {
    app.autolaunch().is_enabled().map_err(|err| err.to_string())
}

#[tauri::command]
fn set_autostart(app: AppHandle, enabled: bool) -> Result<(), String> {
    let manager = app.autolaunch();
    if enabled {
        manager.enable().map_err(|err| err.to_string())
    } else {
        manager.disable().map_err(|err| err.to_string())
    }
}

async fn run_sidecar(
    app: AppHandle,
    args: Vec<String>,
    stdin: Option<String>,
) -> Result<String, String> {
    let sidecar = app
        .shell()
        .sidecar("email-mcp")
        .map_err(|err| err.to_string())?
        .args(args);
    let (mut rx, mut child) = sidecar.spawn().map_err(|err| err.to_string())?;

    if let Some(input) = stdin {
        child
            .write(input.as_bytes())
            .map_err(|err| format!("failed to write sidecar stdin: {err}"))?;
    }

    let mut stdout = Vec::new();
    let mut stderr = Vec::new();
    let mut exit_code = Some(0);

    while let Some(event) = rx.recv().await {
        match event {
            CommandEvent::Stdout(bytes) => stdout.extend(bytes),
            CommandEvent::Stderr(bytes) => stderr.extend(bytes),
            CommandEvent::Error(error) => stderr.extend(error.as_bytes()),
            CommandEvent::Terminated(payload) => exit_code = payload.code,
            _ => {}
        }
    }

    if exit_code.unwrap_or(1) != 0 {
        let detail = String::from_utf8_lossy(&stderr).trim().to_string();
        if detail.is_empty() {
            return Err("sidecar command failed".into());
        }
        return Err(detail);
    }

    Ok(String::from_utf8_lossy(&stdout).trim().to_string())
}

fn temp_json_path(prefix: &str) -> std::path::PathBuf {
    let stamp = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|duration| duration.as_nanos())
        .unwrap_or_default();
    std::env::temp_dir().join(format!("{prefix}-{stamp}.json"))
}

fn setup_tray(app: &App) -> tauri::Result<()> {
    const TRAY_ICON: tauri::image::Image<'static> = tauri::include_image!("./icons/32x32.png");

    let open = MenuItem::with_id(app, "open", "打开设置", true, None::<&str>)?;
    let doctor = MenuItem::with_id(app, "doctor", "运行诊断", true, None::<&str>)?;
    let quit = MenuItem::with_id(app, "quit", "退出", true, None::<&str>)?;
    let menu = Menu::with_items(app, &[&open, &doctor, &quit])?;

    TrayIconBuilder::new()
        .icon(TRAY_ICON)
        .menu(&menu)
        .show_menu_on_left_click(true)
        .on_menu_event(|app, event| match event.id.as_ref() {
            "open" => show_main_window(app),
            "doctor" => {
                show_main_window(app);
                if let Some(window) = app.get_webview_window("main") {
                    let _ = window.emit("run-doctor", ());
                }
            }
            "quit" => app.exit(0),
            _ => {}
        })
        .build(app)?;
    Ok(())
}

fn setup_window_icon(app: &App) -> tauri::Result<()> {
    const WINDOW_ICON: tauri::image::Image<'static> = tauri::include_image!("./icons/128x128.png");

    if let Some(window) = app.get_webview_window("main") {
        window.set_icon(WINDOW_ICON)?;
        #[cfg(windows)]
        set_windows_taskbar_icon(&window)?;
    }
    Ok(())
}

#[cfg(windows)]
fn set_windows_taskbar_icon(window: &tauri::WebviewWindow) -> tauri::Result<()> {
    const TASKBAR_ICON: tauri::image::Image<'static> = tauri::include_image!("./icons/128x128.png");

    let hicon = create_windows_icon(&TASKBAR_ICON)?;
    let hwnd = window.hwnd()?;

    unsafe {
        SendMessageW(
            hwnd,
            WM_SETICON,
            Some(WPARAM(ICON_BIG as usize)),
            Some(LPARAM(hicon.0 as isize)),
        );
    }

    // The HICON must remain valid while Windows uses it for the taskbar. This
    // process-lifetime handle is intentionally not destroyed after WM_SETICON.
    Ok(())
}

#[cfg(windows)]
fn create_windows_icon(
    image: &tauri::image::Image<'_>,
) -> tauri::Result<windows::Win32::UI::WindowsAndMessaging::HICON> {
    let width = i32::try_from(image.width()).map_err(invalid_icon_error)?;
    let height = i32::try_from(image.height()).map_err(invalid_icon_error)?;
    let mut bgra = image.rgba().to_vec();
    let mut and_mask = Vec::with_capacity(bgra.len() / 4);

    for pixel in bgra.chunks_exact_mut(4) {
        and_mask.push(pixel[3].wrapping_sub(u8::MAX));
        pixel.swap(0, 2);
    }

    unsafe { CreateIcon(None, width, height, 1, 32, and_mask.as_ptr(), bgra.as_ptr()) }
        .map_err(invalid_icon_error)
}

#[cfg(windows)]
fn invalid_icon_error(error: impl std::error::Error + Send + Sync + 'static) -> tauri::Error {
    tauri::Error::InvalidIcon(std::io::Error::new(std::io::ErrorKind::Other, error))
}

fn show_main_window(app: &AppHandle) {
    if let Some(window) = app.get_webview_window("main") {
        let _ = window.show();
        let _ = window.set_focus();
    }
}

pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_shell::init())
        .plugin(tauri_plugin_dialog::init())
        .plugin(tauri_plugin_autostart::init(
            tauri_plugin_autostart::MacosLauncher::LaunchAgent,
            Some(vec!["--tray"]),
        ))
        .setup(|app| {
            setup_window_icon(app)?;
            setup_tray(app)?;
            Ok(())
        })
        .invoke_handler(tauri::generate_handler![
            load_config,
            save_config,
            set_secret,
            run_doctor,
            index_status,
            index_sync,
            install_codex,
            restore_codex_backup,
            get_autostart,
            set_autostart
        ])
        .run(tauri::generate_context!())
        .expect("error while running Email MCP desktop");
}
