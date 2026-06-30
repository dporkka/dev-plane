-- wezterm.lua -- Master WezTerm configuration for the Command Tower.
--
-- Features:
--   * WebGpu frontend capped at 60 FPS.
--   * 10,000-line scrollback, Catppuccin Mocha, JetBrainsMono Nerd Font.
--   * Tailscale-bound multiplexer domains for WSL2, Mini-PC, and Contabo VPS.
--   * Leader keymaps for pane/window management.
--   * CMD+SHIFT+J "Worktree Teleportation" to /mnt/agent-swarms/{task_id}.
--   * OSC 52 clipboard integration and OSC 777 bell-driven push alerts.

local wezterm = require("wezterm")
local act = wezterm.action
local config = wezterm.config_builder and wezterm.config_builder() or {}

-- ---------------------------------------------------------------------------
-- Front-end rendering
-- ---------------------------------------------------------------------------
config.front_end = "WebGpu"
config.max_fps = 60
config.animation_fps = 60
config.scrollback_lines = 10000
config.enable_scroll_bar = true

-- Color scheme and font.
config.color_scheme = "Catppuccin Mocha"
config.font = wezterm.font("JetBrainsMono Nerd Font", { weight = "Medium" })
config.font_size = 12.0
config.line_height = 1.2

-- ---------------------------------------------------------------------------
-- Window / tab appearance
-- ---------------------------------------------------------------------------
config.window_decorations = "RESIZE"
config.window_padding = { left = 4, right = 4, top = 4, bottom = 4 }
config.use_fancy_tab_bar = true
config.tab_bar_at_bottom = true
config.hide_tab_bar_if_only_one_tab = false
config.show_new_tab_button_in_tab_bar = true

-- ---------------------------------------------------------------------------
-- Leader key
-- ---------------------------------------------------------------------------
config.leader = { key = "Space", mods = "CTRL", timeout_milliseconds = 1000 }

-- ---------------------------------------------------------------------------
-- Multiplexer domains -- all remote mux servers are reached over Tailscale.
-- ---------------------------------------------------------------------------
config.unix_domains = {
  {
    name = "local",
  },
}

config.ssh_domains = {
  {
    name = "wsl2",
    remote_address = "100.64.0.2",
    -- username defaults to the local user; override if your WSL user differs.
  },
  {
    name = "mini-pc",
    remote_address = "100.64.0.3",
  },
  {
    name = "contabo-vps",
    remote_address = "100.64.0.10",
  },
}

config.default_gui_startup_args = { "connect", "local" }
config.mux_enforce_ssh_agent = false

-- ---------------------------------------------------------------------------
-- CMD+SHIFT+J Worktree Teleportation
--
-- Prompts for a Swarm Task ID, computes the remote RAM-disk path
-- /mnt/agent-swarms/{task_id}, and opens a split in the Contabo VPS domain
-- running Neovim in that directory.
-- ---------------------------------------------------------------------------
config.keys = {
  {
    key = "J",
    mods = "CMD|SHIFT",
    action = wezterm.action_callback(function(window, pane)
      window:perform_action(
        act.PromptInputLine({
          description = "Swarm Task ID",
          action = wezterm.action_callback(function(inner_window, inner_pane, line)
            if not line or line == "" then
              return
            end
            local task_id = line:gsub("^%s*(.-)%s*$", "%1")
            local remote_path = "/mnt/agent-swarms/" .. task_id
            inner_window:perform_action(
              act.SplitPane({
                direction = "Right",
                size = { Percent = 50 },
                domain = { DomainName = "contabo-vps" },
                command = {
                  cwd = remote_path,
                  args = { "nvim", "." },
                },
              }),
              inner_pane
            )
          end),
        }),
        pane
      )
    end),
  },

  -- Leader keymaps: tabs, panes, and windows.
  { key = "c", mods = "LEADER", action = act.SpawnTab("CurrentPaneDomain") },
  { key = "x", mods = "LEADER", action = act.CloseCurrentTab({ confirm = true }) },
  { key = "n", mods = "LEADER", action = act.ActivateTabRelative(1) },
  { key = "p", mods = "LEADER", action = act.ActivateTabRelative(-1) },
  { key = "|", mods = "LEADER", action = act.SplitHorizontal({ domain = "CurrentPaneDomain" }) },
  { key = "-", mods = "LEADER", action = act.SplitVertical({ domain = "CurrentPaneDomain" }) },
  { key = "h", mods = "LEADER", action = act.ActivatePaneDirection("Left") },
  { key = "j", mods = "LEADER", action = act.ActivatePaneDirection("Down") },
  { key = "k", mods = "LEADER", action = act.ActivatePaneDirection("Up") },
  { key = "l", mods = "LEADER", action = act.ActivatePaneDirection("Right") },
  { key = "z", mods = "LEADER", action = act.TogglePaneZoomState },
  { key = "w", mods = "LEADER", action = act.SpawnWindow },
  { key = "r", mods = "LEADER", action = act.ReloadConfiguration },
  { key = "q", mods = "LEADER", action = act.QuitApplication },

  -- Clipboard (OSC 52 keeps the Windows host clipboard hydrated from yanks).
  { key = "c", mods = "CMD", action = act.CopyTo("ClipboardAndPrimarySelection") },
  { key = "v", mods = "CMD", action = act.PasteFrom("Clipboard") },
}

-- ---------------------------------------------------------------------------
-- OSC 52 clipboard integration
-- ---------------------------------------------------------------------------
config.enable_wayland = true
config.enable_csi_u_key_encoding = true
config.allow_win32_input_mode = false

config.mouse_bindings = {
  {
    event = { Down = { streak = 1, button = "Middle" } },
    mods = "NONE",
    action = act.PasteFrom("PrimarySelection"),
  },
}

-- ---------------------------------------------------------------------------
-- OSC 777 push alerts on bell events
--
-- Worker errors and manual triage boundaries can ring the bell; turn that
-- into a native desktop notification.
-- ---------------------------------------------------------------------------
wezterm.on("bell", function(window, pane)
  -- toast_notification is available in recent WezTerm nightly/stable builds.
  -- Fall back silently on older builds to keep the config portable.
  if not window.toast_notification then
    return
  end
  local domain = pane.get_domain_name and pane:get_domain_name() or "unknown"
  window:toast_notification(
    "Command Tower Alert",
    "Worker error or manual triage boundary triggered (domain: " .. domain .. ")",
    nil,
    4000
  )
end)

config.audible_bell = "Disabled"
config.visual_bell = {
  fade_in_function = "EaseIn",
  fade_in_duration_ms = 75,
  fade_out_function = "EaseOut",
  fade_out_duration_ms = 75,
}

-- ---------------------------------------------------------------------------
-- Ambient status line -- node health from local cache file.
-- ---------------------------------------------------------------------------
local NODE_HEALTH_FILE = (os.getenv("HOME") or "/tmp") .. "/.cache/dev-plane/node-health.json"

local function read_node_health()
  local f = io.open(NODE_HEALTH_FILE, "r")
  if not f then
    return nil
  end
  local data = f:read("*a")
  f:close()
  if not data or data == "" then
    return nil
  end
  local ok, parsed = pcall(wezterm.json_parse, data)
  if not ok then
    return nil
  end
  return parsed
end

wezterm.on("update-status", function(window, pane)
  local health = read_node_health()
  if not health then
    window:set_right_status(wezterm.format({
      { Foreground = { Color = "#f38ba8" } },
      { Text = " no-health " },
    }))
    return
  end

  local status = health.status or "unknown"
  local nodes = health.nodes or 0
  local agents = health.agents or 0
  local load = health.load_avg and health.load_avg[1] or "?"
  local color = "#a6e3a1"
  if status == "degraded" then
    color = "#f9e2af"
  elseif status == "unhealthy" then
    color = "#f38ba8"
  end

  local text = string.format(" %s | nodes:%d agents:%d load:%s ", status, nodes, agents, load)
  window:set_right_status(wezterm.format({
    { Foreground = { Color = color } },
    { Text = text },
  }))
end)

-- ---------------------------------------------------------------------------
-- Hyperlink and bell rules
-- ---------------------------------------------------------------------------
config.hyperlink_rules = wezterm.default_hyperlink_rules()

return config
