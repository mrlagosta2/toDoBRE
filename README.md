# 📝 MAU-toDoTUI

A minimalist yet powerful Terminal User Interface (TUI) for managing tasks, built with **Go**, **Bubble Tea**, and **Lip Gloss**.

![MAU-toDoTUI](https://charm.sh/img/bubbletea.png)
*(Note: Replace with actual screenshot if available)*

## ✨ Features

- **📂 Group Management**: Organize tasks into custom groups (Work, Personal, etc.).
- **🌐 "All" Aggregation**: The default "All" group (Index 0) shows every task from every group in one timeline.
- **🎨 Dynamic Theming**:
    - Over 20+ distinct pastel and neon colors for groups.
    - Borders and highlights dynamically match the active group's color.
    - The "All" view is neutrally styled (Light Gray) for distinction.
- **⚡ Smart Sorting**: Active tasks float to the top; Completed tasks sink to the bottom (dimmed).
- **📝 Full CRUD**:
    - **Create**: Add new Groups or Tasks instantly.
    - **Read**: Filter by group or view all.
    - **Update**: **Rename** groups and tasks in place with context-aware logic.
    - **Delete**: Safe deletion with confirmation for non-empty groups.
- **👁️ View Modes**: Toggle between **Full Mode** (Bordered Window) and **Compact Mode** (Inline) using `Tab`.

---

## 🚀 Installation & Usage

### Prerequisites
- [Go](https://go.dev/dl/) installed.

### Run from Source
```bash
# Clone the repository
git clone https://github.com/mauvernaz/todoTUI.git
cd todoTUI

# Run directly
go run .

# Or build the executable
go build -o todo.exe
./todo.exe
```

### 🌍 Global Access (Recommended)
To run `todo` from anywhere, add the directory containing `todo.exe` to your system's **PATH**.

**Windows Users:**
1. Search for **"Edit environment variables for your account"**.
2. Select the `Path` variable and click **Edit**.
3. Click **New** and paste the full path to your `todoTUI` folder (e.g., `C:\Users\YourName\Documents\todoTUI`).
4. **Restart your terminal.** Now you can type `todo` from any folder!

---

## ⌨️ Controls

The application uses **Vim-style** navigation alongside standard Arrow keys.

### 🧭 Navigation
| Key | Action |
| :--- | :--- |
| **`Tab`** | **Toggle View Mode** (Full Window / Compact) |
| **`←`** / **`h`** | Go to **Group List** |
| **`→`** / **`l`** | Enter **selected Group** (Task View) |
| **`Enter`** | Enter **selected Group** (Task View) |
| **`↑`** / **`k`** | Move Cursor Up |
| **`↓`** / **`j`** | Move Cursor Down |

### ⚡ Actions
| Key | Context | Action |
| :--- | :--- | :--- |
| **`n`** | Any | **New** Group (if in Group List) or Task (if in Task List) |
| **`r`** | Any | **Rename** Selected Group or Task |
| **`x`** | Any | **Delete** Selected Item |
| **`Space`** | Tasks | **Toggle Done/Undone** |
| **`Ctrl+x`** | Deletion | **Confirm Deletion** (for groups with tasks) |
| **`Esc`** | Input | **Cancel** Input / Return to List |
| **`q`** | Global | **Quit** Application |

---

## 💾 Data Persistence

Your data is automatically saved to a JSON file in your user configuration directory:
- **Windows**: `C:\Users\%USERNAME%\AppData\Roaming\todotui\todos.json`
- **Linux/Mac**: `~/.config/todotui/todos.json`

The file is human-readable and can be backed up or edited manually if needed.

---

## 🛠️ Built With

- **Language**: [Go](https://go.dev/)
- **TUI Framework**: [Bubble Tea](https://github.com/charmbracelet/bubbletea)
- **Styling**: [Lip Gloss](https://github.com/charmbracelet/lipgloss)
- **Input Components**: [Bubbles](https://github.com/charmbracelet/bubbles)

---

## 📄 License

MIT License. Free to use and modify.
