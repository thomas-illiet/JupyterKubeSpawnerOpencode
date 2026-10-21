/** @jsxImportSource @opentui/solid */
import type { TuiPlugin, TuiPluginModule } from "@opencode-ai/plugin/tui"

const open = [
  "█▀▀█ █▀▀█ █▀▀▀ █▄  █",
  "█  █ █▀▀▀ █▀▀  █ █ █",
  "▀▀▀▀ ▀    ▀▀▀▀ ▀  ▀▀",
]

const cloud = [
  "█▀▀▀ █    █▀▀█ █  █ █▀▀▄",
  "█    █    █  █ █  █ █  █",
  "▀▀▀▀ ▀▀▀▀ ▀▀▀▀ ▀▀▀▀ ▀▀▀ ",
]

const tui: TuiPlugin = async (api) => {
  api.slots.register({
    slots: {
      home_logo(ctx) {
        const openColor = ctx.theme.current.primary
        const cloudColor = ctx.theme.current.secondary
        return (
          <box flexDirection="column">
            {open.map((line, index) => (
              <text>
                <span style={{ fg: openColor }}>{line}</span>
                <span style={{ fg: cloudColor }}>  {cloud[index]}</span>
              </text>
            ))}
          </box>
        )
      },
    },
  })
}

const plugin: TuiPluginModule & { id: string } = {
  id: "opencloud-branding",
  tui,
}

export default plugin
