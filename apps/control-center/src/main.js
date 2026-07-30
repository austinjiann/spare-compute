const { app, BrowserWindow, ipcMain, shell } = require("electron");
const { execFile } = require("node:child_process");
const path = require("node:path");

const repoRoot = path.resolve(__dirname, "..", "..", "..");

function createWindow() {
  const win = new BrowserWindow({
    width: 720,
    height: 640,
    minWidth: 560,
    minHeight: 520,
    title: "ComputeHop Control Center",
    backgroundColor: "#00000000",
    transparent: true,
    vibrancy: "under-window",
    visualEffectState: "active",
    titleBarStyle: "hiddenInset",
    trafficLightPosition: { x: 16, y: 18 },
    webPreferences: {
      preload: path.join(__dirname, "preload.js"),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: false
    }
  });

  win.loadFile(path.join(__dirname, "index.html"));
}

app.whenReady().then(() => {
  createWindow();

  app.on("activate", () => {
    if (BrowserWindow.getAllWindows().length === 0) {
      createWindow();
    }
  });
});

app.on("window-all-closed", () => {
  if (process.platform !== "darwin") {
    app.quit();
  }
});

ipcMain.handle("devices:list", async () => {
  const result = await runComputeHop(["devices"]);
  return {
    ok: result.ok,
    error: result.error,
    raw: result.stdout,
    devices: result.ok ? parseDevices(result.stdout) : []
  };
});

ipcMain.handle("app:openExternal", async (_event, target) => {
  if (typeof target !== "string") {
    return;
  }
  await shell.openExternal(target);
});

async function runComputeHop(args) {
  const installed = await execCommand("computehop", args, process.cwd());
  if (installed.ok) {
    return installed;
  }

  return execCommand("go", ["run", "./cmd/computehop", ...args], repoRoot);
}

function execCommand(command, args, cwd) {
  return new Promise((resolve) => {
    execFile(
      command,
      args,
      {
        cwd,
        timeout: 7000,
        windowsHide: true
      },
      (error, stdout, stderr) => {
        if (error) {
          resolve({
            ok: false,
            stdout: stdout || "",
            error: (stderr || error.message || "Command failed").trim()
          });
          return;
        }
        resolve({ ok: true, stdout: stdout.trim(), error: "" });
      }
    );
  });
}

function parseDevices(stdout) {
  if (!stdout) {
    return [];
  }

  const lines = stdout
    .split(/\r?\n/)
    .map((line) => line.trimEnd())
    .filter(Boolean);

  if (lines.length <= 1) {
    return [];
  }

  return lines
    .slice(1)
    .map((line) => parseDeviceLine(line))
    .filter(Boolean);
}

function parseDeviceLine(line) {
  if (
    line === "Next:" ||
    line.startsWith("- ") ||
    line.startsWith("No connected or nearby devices.") ||
    line.startsWith("LAN discovery")
  ) {
    return null;
  }

  const columns = line.split(/\s{2,}/).map((column) => column.trim());
  if (columns.length >= 8) {
    return {
      name: columns[0],
      id: columns[1],
      connection: columns[2],
      role: columns[3],
      availability: columns[4],
      path: columns[5],
      address: columns[6],
      updated: columns[7]
    };
  }

  if (columns.length >= 6) {
    return {
      name: columns[0],
      id: columns[1],
      connection: columns[2],
      role: columns[3],
      availability: columns[2],
      path: "",
      address: columns[4],
      updated: columns[5]
    };
  }

  return null;
}
