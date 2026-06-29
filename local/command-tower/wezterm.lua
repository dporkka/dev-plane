-- wezterm.lua -- Master WezTerm configuration for the Command Tower.
--
-- Features:
--   * WebGpu frontend, capped at 60 FPS.
--   * 10,000-line scrollback.
--   * Leader keymaps for window/tab/workspace management.
--   * CMD+SHIFT+J "Worktree Teleportation" macro.
--   * OSC 52 clipboard integration.
--   * Ambient status line pulled from a local node-health cache file.

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
-- Leader key and keymaps
-- ---------------------------------------------------------------------------
config.leader = { key = "Space", mods = "CTRL", timeout_milliseconds = 1000 }

config.keys = {
  -- Worktree Teleportation: CMD+SHIFT+J
  {
    key = "J",
    mods = "CMD|SHIFT",
    action = wezterm.action_callback(function(window, pane)
      local home = os.getenv("HOME") or "/home/user"
      local candidates = {}
      -- Collect known worktree roots from a flat directory.
      local worktree_root = home .. "/worktrees"
      local handle = io.popen('ls -1 "' .. worktree_root .. '" 2>/dev/null')
      if handle then
        for name in handle:lines() do
          table.insert(candidates, { label = name, id = worktree_root .. "/" .. name })
        end
        handle:close()
      end

      window:perform_action(
        act.InputSelector({
          action = wezterm.action_callback(function(inner_window, inner_pane, id, label)
            if not id then
              return
            end
            -- Spawn a new tab in the selected worktree.
            inner_window:perform_action(
              act.SpawnCommandInNewTab({
                cwd = id,
                args = { os.getenv("SHELL") or "/bin/bash", "-l" },
                set_environment_variables = {
                  DEV_PLANE_WORKTREE = label,
                },
              }),
              inner_pane
            )
          end),
          title = "Worktree Teleportation",
          choices = candidates,
          fuzzy = true,
        }),
        pane
      )
    end),
  },

  -- Leader keymaps
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
  { key = "r", mods = "LEADER", action = act.ReloadConfiguration },
  { key = "q", mods = "LEADER", action = act.QuitApplication },

  -- Clipboard via OSC 52 (explicit for terminals that need a nudge).
  { key = "c", mods = "CMD", action = act.CopyTo("ClipboardAndPrimarySelection") },
  { key = "v", mods = "CMD", action = act.PasteFrom("Clipboard") },
}

-- ---------------------------------------------------------------------------
-- OSC 52 clipboard integration
-- ---------------------------------------------------------------------------
config.enable_wayland = true
config.enable_csi_u_key_encoding = true
config.allow_win32_input_mode = false

-- OSC 52: allow both setting and querying the clipboard from remote hosts.
config.mouse_bindings = {
  {
    event = { Down = { streak = 1, button = "Middle" } },
    mods = "NONE",
    action = act.PasteFrom("PrimarySelection"),
  },
}

-- ---------------------------------------------------------------------------
-- Multiplexer domain (Tailscale-bound wezterm-mux-server)
-- ---------------------------------------------------------------------------
config.unix_domains = {
  {
    name = "local",
  },
}

config.ssh_domains = {}
config.tls_clients = {}

-- Optional: connect to a remote wezterm-mux-server over Tailscale.
config.mux_enforce_ssh_agent = false
config.default_gui_startup_args = { "connect", "local" }

-- ---------------------------------------------------------------------------
-- Ambient status line -- node health from local cache file.
-- ---------------------------------------------------------------------------
local NODE_HEALTH_FILE = os.getenv("HOME") .. "/.cache/dev-plane/node-health.json"

local function read_node_health()
  local f = io.open(NODE_HEALTH_FILE, "r")
  if not f then
    return nil
  end
  local data = f:read("*a")
  f:close()
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
config.audible_bell = "Disabled"
config.visual_bell = {
  fade_in_function = "EaseIn",
  fade_in_duration_ms = 75,
  fade_out_function = "EaseOut",
  fade_out_duration_ms = 75,
}

return config
