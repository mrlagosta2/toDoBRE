# 📝 MAU-toDoTUI

A minimalist yet powerful Terminal User Interface (TUI) for managing tasks, built with **Go**, **Bubble Tea**, and **Lip Gloss**.

![MAU-toDoTUI](https://charm.sh/img/bubbletea.png)
*(Note: Replace with actual screenshot if available)*

## ✨ Features

- **🗂️ 4-Level Data Hierarchy**:
    - **Workspaces**: High-level directories (e.g., Work, Personal, Projects).
    - **Groups**: Custom files to categorize your task lists.
    - **Tasks**: Individual to-dos with detailed descriptions and metadata.
    - **Subtasks**: Break down complex tasks into easily manageable steps.
- **🌐 Smart Views & Virtual Groups**:
    - **"ALL" Group**: An aggregated view of every task across your current workspace.
    - **"FAVORITES" Group**: Track and pin your favorite groups (Toggle with `f`).
    - **Global "TODAY" View**: Press `T` from the workspaces view to instantly see all tasks marked for "Today" across all your workspaces.
- **📝 Rich Task Management**:
    - **Descriptions**: Add detailed multi-line notes or descriptions to tasks (`d`), previewable directly from the task list.
    - **Subtasks**: Create, reorder, and toggle individual subtasks for deeper organization.
    - **Custom Ordering**: Reorder workspaces, groups, tasks, and subtasks seamlessly using `Shift+Up` and `Shift+Down`.
- **💻 Integrated Git Console**:
    - Built-in console (`g`) allows you to run git commands (e.g., `git status`, `git commit`) directly within the TUI to transparently version control your task files.
- **🎨 Dynamic Theming & Views**:
    - Over 20+ distinct pastel and neon colors for various groups.
    - Borders and highlights dynamically match the active group's color.
    - Toggle between **Full Mode** (Bordered Window) and **Compact Mode** (Inline) instantly using `Tab`.
    - Smart sorting automatically floats active tasks to the top and sinks completed tasks to the bottom (dimmed).

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

The application uses intuitive **Vim-style** navigation alongside standard Arrow keys.

### 🧭 Navigation & Views
| Key | Context | Action |
| :--- | :--- | :--- |
| **`Tab`** | Global | **Toggle View Mode** (Full Window / Compact) |
| **`←`** / **`h`** | Any | Go **Back** / Up a level (e.g., Tasks -> Groups) |
| **`→`** / **`l`** | Any | **Drill Down** / Enter selected item |
| **`Enter`** | Any | **Drill Down** (Groups, Tasks, Workspaces, Subtasks) |
| **`↑`** / **`k`** | Any | Move Cursor **Up** |
| **`↓`** / **`j`** | Any | Move Cursor **Down** |
| **`T`** | Workspaces | View Global **Today's Tasks** |
| **`g`** | Global | Open builtin **Git Console** |

### ⚡ Actions
| Key | Context | Action |
| :--- | :--- | :--- |
| **`n`** | Any | **New** (Workspace, Group, Task, Subtask) |
| **`r`** | Any | **Rename** Selected Item (or Subtask) |
| **`R`** | Task Details | **Rename Parent Task Title** (when highlighting subtasks) |
| **`d`** | Task Details | Add / Edit **Task Description** |
| **`f`** | Groups List | Toggle **Favorite** status for a Group |
| **`t`** | Tasks List | Mark / Unmark Task for **Today** |
| **`Shift+↑`** / **`K`** | Any | **Move Item Up** (Reorder) |
| **`Shift+↓`** / **`J`** | Any | **Move Item Down** (Reorder) |
| **`Space`** | Tasks / Subtasks | **Toggle Done / Undone** |
| **`ctrl+x`** | Any | **Delete** Selected Item (or confirm deletion) |
| **`Esc`** | Input / Detail | **Cancel** Input / Return to previous view |
| **`q`** | Global | **Quit** Application |

---

## 💾 Data Persistence

Your data is automatically saved to clear, human-readable JSON files in an organized folder structure inside your user configuration directory:
- **Windows**: `C:\Users\%USERNAME%\AppData\Roaming\todotui\workspaces\`
- **Linux/Mac**: `~/.config/todotui/workspaces/`

Each workspace is a directory containing individual `.json` files for groups, along with a `meta.json` file storing your custom order preferences and favorite toggles. The structural simplicity makes it extremely easy to back up or version control via Git.

---

## 🛠️ Built With

- **Language**: [Go](https://go.dev/)
- **TUI Framework**: [Bubble Tea](https://github.com/charmbracelet/bubbletea)
- **Styling**: [Lip Gloss](https://github.com/charmbracelet/lipgloss)
- **Input Components**: [Bubbles](https://github.com/charmbracelet/bubbles)

---

## 📄 License

MIT License. Free to use and modify.
