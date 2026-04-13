# Plano de Implementacao - todoTUI Sync (Nextcloud)

## Objetivo

Permitir que dois ou mais usuarios compartilhem o diretorio de dados do todoTUI
via Nextcloud (ou qualquer sync de arquivos) sem perda de dados, com hot-reload
automatico e merge inteligente de conflitos.

---

## Visao Geral da Arquitetura Atual

```
User Input -> Update() -> handler -> saveGroupFile() -> os.WriteFile() (JSON inteiro)
                                  -> loadGroupFile() -> os.ReadFile()  (JSON inteiro)
```

**Problemas atuais para uso compartilhado:**
1. `saveGroupFile()` sobrescreve o JSON inteiro — se dois usuarios editam ao mesmo tempo, o ultimo ganha
2. Nao ha deteccao de mudancas externas — dados carregados em memoria ficam stale
3. Nao ha nenhum file locking
4. Arquivos de conflito do Nextcloud (`(conflicted copy)`) sao ignorados

---

## Plano em 5 Fases

### FASE 1: Configuracao do Data Dir Customizado
**Prioridade: ALTA | Complexidade: BAIXA**

Atualmente o `getDataDir()` retorna um caminho fixo relativo ao executavel.
Para compartilhar via Nextcloud, o usuario precisa poder apontar para uma pasta sincronizada.

**Mudancas em `main.go`:**

1. Criar arquivo de configuracao `config.json` em `%AppData%/todotui/config.json`:
```go
type AppConfig struct {
    DataDir string `json:"data_dir,omitempty"` // se vazio, usa o padrao
}
```

2. Alterar `getDataDir()` para ler o config primeiro:
```go
func getDataDir() string {
    cfg := loadAppConfig()
    if cfg.DataDir != "" {
        return cfg.DataDir
    }
    // fallback para o comportamento atual
    exe, err := os.Executable()
    if err != nil {
        return "todo_data"
    }
    return filepath.Join(filepath.Dir(exe), "todo_data")
}
```

3. Adicionar comando no programa para configurar (ou editar manualmente o config.json):
   - Nova tecla `S` na tela de workspaces -> abre prompt para definir data_dir
   - Ou simplesmente documentar que o usuario edite o config.json

**Exemplo de config.json:**
```json
{
  "data_dir": "C:\\Users\\Matheus\\Nextcloud\\todoTUI_shared"
}
```

**Arquivos afetados:** `main.go` linhas 179-185 (getDataDir)

---

### FASE 2: File Hashing + Deteccao de Mudancas Externas
**Prioridade: ALTA | Complexidade: MEDIA**

O nucleo da solucao: detectar quando um arquivo foi modificado externamente
(pelo Nextcloud sincronizando mudancas do outro usuario).

**2.1 - Adicionar hash tracking ao model:**

```go
// Novo campo no model (linha ~96)
type model struct {
    // ... campos existentes ...

    // Sync: hash do arquivo no momento do ultimo load
    fileHashes map[string]string // chave: "workspace/group" -> hash MD5 do conteudo
}
```

Inicializar no `initialModel()`:
```go
fileHashes: make(map[string]string),
```

**2.2 - Funcao helper para calcular hash:**

```go
import "crypto/md5"

func fileHash(path string) string {
    data, err := os.ReadFile(path)
    if err != nil {
        return ""
    }
    return fmt.Sprintf("%x", md5.Sum(data))
}

func groupFilePath(workspace, group string) string {
    return filepath.Join(getDataDir(), workspace, group+".json")
}
```

**2.3 - Gravar hash ao carregar:**

Alterar `loadGroupFile()` (linha 325) para retornar tambem o hash:

```go
func loadGroupFile(workspace, group string) (GroupFile, string) {
    path := groupFilePath(workspace, group)
    data, err := os.ReadFile(path)
    if err != nil {
        return GroupFile{Title: group, Todos: []Todo{}}, ""
    }
    hash := fmt.Sprintf("%x", md5.Sum(data))
    var gf GroupFile
    if err := json.Unmarshal(data, &gf); err != nil {
        return GroupFile{Title: group, Todos: []Todo{}}, ""
    }
    return gf, hash
}
```

**2.4 - Atualizar TODOS os call sites de `loadGroupFile`:**

Existem 11 chamadas a `loadGroupFile` no codigo. Cada uma precisa ser
atualizada para tambem gravar o hash:

| Linha | Contexto | Mudanca |
|-------|----------|---------|
| 381   | `countGroupTasks` | Nao precisa de hash (read-only count) — manter assinatura separada ou ignorar hash |
| 505   | `buildAllViewItems` | Nao precisa de hash (read-only) |
| 521   | `buildFavViewItems` | Nao precisa de hash (read-only) |
| 537   | `buildTodayItems` | Nao precisa de hash (read-only) |
| 677   | `handleInput` (rename group) | Precisa de hash |
| 901   | `handleGroups` (enter group) | Precisa de hash -> `m.fileHashes[ws+"/"+group] = hash` |
| 1089  | `handleAllViewTasks` (toggle) | Precisa de hash |
| 1130  | `handleTodayView` (toggle) | Precisa de hash |
| 1144  | `handleTodayView` (un-today) | Precisa de hash |
| 1553  | `viewAllTasks` (render) | Nao precisa de hash (read-only) |
| 1613  | `viewToday` (render) | Nao precisa de hash (read-only) |

**Estrategia:** Criar duas funcoes:
- `loadGroupFile(workspace, group string) GroupFile` — para leituras read-only (views, counts)
- `loadGroupFileTracked(workspace, group string) (GroupFile, string)` — para leituras que precedem writes

---

### FASE 3: Save com Merge Inteligente (Read-Before-Write)
**Prioridade: ALTA | Complexidade: ALTA**

Esta e a fase mais critica. Antes de cada `saveGroupFile`, reler o disco e
fazer merge se houve mudanca externa.

**3.1 - Nova funcao `safeSaveGroupFile`:**

```go
func (m *model) safeSaveGroupFile(workspace, group string, localTasks GroupFile) GroupFile {
    key := workspace + "/" + group
    savedHash := m.fileHashes[key]

    // Ler estado atual do disco
    diskFile, diskHash := loadGroupFileTracked(workspace, group)

    // Se o hash nao mudou, o arquivo nao foi tocado externamente -> save direto
    if diskHash == savedHash || savedHash == "" {
        saveGroupFile(workspace, group, localTasks)
        // Atualizar hash apos save
        newPath := groupFilePath(workspace, group)
        m.fileHashes[key] = fileHash(newPath)
        return localTasks
    }

    // Hash mudou! Houve edicao externa -> merge
    merged := mergeGroupFiles(diskFile, localTasks, m.lastKnownState[key])
    saveGroupFile(workspace, group, merged)
    m.fileHashes[key] = fileHash(groupFilePath(workspace, group))
    return merged
}
```

**3.2 - Guardar "base state" para three-way merge:**

Adicionar ao model:
```go
lastKnownState map[string]GroupFile // estado no momento do load (base para merge)
```

Ao carregar um grupo:
```go
gf, hash := loadGroupFileTracked(workspace, group)
m.fileHashes[key] = hash
m.lastKnownState[key] = deepCopyGroupFile(gf) // copia profunda
m.tasks = gf
```

**3.3 - Algoritmo de merge (three-way):**

```go
// mergeGroupFiles faz um three-way merge entre:
//   - disk:  versao atual no disco (editada pelo outro usuario)
//   - local: versao local com as mudancas deste usuario
//   - base:  versao original antes de qualquer edicao
func mergeGroupFiles(disk, local, base GroupFile) GroupFile {
    result := GroupFile{Title: local.Title}

    // Indexar todos por titulo (chave primaria)
    baseMap  := todoMapByTitle(base.Todos)
    diskMap  := todoMapByTitle(disk.Todos)
    localMap := todoMapByTitle(local.Todos)

    seen := map[string]bool{}

    // 1. Processar todos que existem no LOCAL (preserva ordem local)
    for _, lt := range local.Todos {
        seen[lt.Title] = true
        dt, onDisk := diskMap[lt.Title]
        bt, onBase := baseMap[lt.Title]

        if !onDisk && onBase {
            // Existia na base, nao esta no disco -> outro usuario deletou
            // Conflito: local ainda tem. Decisao: manter (priorizar nao perder dados)
            result.Todos = append(result.Todos, lt)
            continue
        }

        if !onDisk && !onBase {
            // Novo no local, nao existe no disco -> adicionar
            result.Todos = append(result.Todos, lt)
            continue
        }

        // Existe em ambos -> merge campo a campo
        merged := mergeTodo(bt, dt, lt)
        result.Todos = append(result.Todos, merged)
    }

    // 2. Adicionar todos que estao no DISCO mas NAO no LOCAL
    for _, dt := range disk.Todos {
        if seen[dt.Title] {
            continue
        }
        _, onBase := baseMap[dt.Title]
        if !onBase {
            // Novo no disco (outro usuario adicionou) -> incluir
            result.Todos = append(result.Todos, dt)
        }
        // Se estava na base e nao esta no local -> usuario local deletou -> nao incluir
    }

    return result
}

func mergeTodo(base, disk, local Todo) Todo {
    result := local // comeca com local como base

    // Done: se local nao mudou em relacao a base, usar disco
    if local.Done == base.Done && disk.Done != base.Done {
        result.Done = disk.Done
    }

    // Description: se local nao mudou, usar disco
    if local.Description == base.Description && disk.Description != base.Description {
        result.Description = disk.Description
    }

    // Today: se local nao mudou, usar disco
    if local.Today == base.Today && disk.Today != base.Today {
        result.Today = disk.Today
    }

    // Subtasks: merge por titulo (mesma logica)
    result.Subtasks = mergeSubtasks(base.Subtasks, disk.Subtasks, local.Subtasks)

    return result
}

func mergeSubtasks(base, disk, local []Subtask) []Subtask {
    baseMap  := subtaskMapByTitle(base)
    diskMap  := subtaskMapByTitle(disk)

    result := []Subtask{}
    seen := map[string]bool{}

    for _, ls := range local {
        seen[ls.Title] = true
        ds, onDisk := diskMap[ls.Title]
        bs, onBase := baseMap[ls.Title]

        if !onDisk && onBase {
            result = append(result, ls) // deletado remotamente, mantemos local
            continue
        }
        if !onDisk {
            result = append(result, ls) // novo local
            continue
        }

        // Merge done status
        merged := ls
        if ls.Done == bs.Done && ds.Done != bs.Done {
            merged.Done = ds.Done
        }
        result = append(result, merged)
    }

    // Adicionar novos do disco
    for _, ds := range disk {
        if !seen[ds.Title] {
            _, onBase := baseMap[ds.Title]
            if !onBase {
                result = append(result, ds) // novo remoto
            }
        }
    }

    return result
}
```

**3.4 - Helper maps:**

```go
func todoMapByTitle(todos []Todo) map[string]Todo {
    m := make(map[string]Todo, len(todos))
    for _, t := range todos {
        m[t.Title] = t
    }
    return m
}

func subtaskMapByTitle(subs []Subtask) map[string]Subtask {
    m := make(map[string]Subtask, len(subs))
    for _, s := range subs {
        m[s.Title] = s
    }
    return m
}

func deepCopyGroupFile(gf GroupFile) GroupFile {
    data, _ := json.Marshal(gf)
    var copy GroupFile
    _ = json.Unmarshal(data, &copy)
    return copy
}
```

**3.5 - Substituir TODAS as chamadas de `saveGroupFile` por `safeSaveGroupFile`:**

Existem 14 chamadas a `saveGroupFile` no codigo (excluindo backup/migration):

| Linha | Contexto |
|-------|----------|
| 301   | `loadGroupsWithMeta` — salva meta reconciliado, nao precisa merge de tasks |
| 345   | `saveGroupFile` interno — este e o metodo base, NAO alterar |
| 648   | `handleInput` inputAddGroup — novo grupo, nao tem conflito |
| 654   | `handleInput` inputAddTask — **PRECISA merge** |
| 659   | `handleInput` inputAddSubtask — **PRECISA merge** |
| 679   | `handleInput` inputRenameGroup — caso especial (renomeia arquivo) |
| 697   | `handleInput` inputRenameTask — **PRECISA merge** |
| 703   | `handleInput` inputEditDescription — **PRECISA merge** |
| 710   | `handleInput` inputRenameSubtask — **PRECISA merge** |
| 760   | `handleConfirmDelete` (task) — **PRECISA merge** |
| 770   | `handleConfirmDelete` (subtask) — **PRECISA merge** |
| 1006  | `handleTasks` (toggle done) — **PRECISA merge** |
| 1043  | `handleTasks` (reorder up) — **PRECISA merge** |
| 1049  | `handleTasks` (reorder down) — **PRECISA merge** |
| 1054  | `handleTasks` (toggle today) — **PRECISA merge** |
| 1092  | `handleAllViewTasks` (toggle) — **PRECISA merge** |
| 1133  | `handleTodayView` (toggle done) — **PRECISA merge** |
| 1147  | `handleTodayView` (un-today) — **PRECISA merge** |
| 1181  | `handleTaskDetails` (toggle subtask) — **PRECISA merge** |
| 1218  | `handleTaskDetails` (delete subtask) — **PRECISA merge** |
| 1224  | `handleTaskDetails` (reorder subtask up) — **PRECISA merge** |
| 1230  | `handleTaskDetails` (reorder subtask down) — **PRECISA merge** |

Para cada chamada marcada com "PRECISA merge":
```go
// ANTES:
saveGroupFile(m.currentWorkspace, m.currentGroup, m.tasks)

// DEPOIS:
m.tasks = m.safeSaveGroupFile(m.currentWorkspace, m.currentGroup, m.tasks)
```

Para as chamadas em `handleAllViewTasks` e `handleTodayView` que operam sobre
grupos diferentes do `m.tasks`, a logica sera:
```go
gf, hash := loadGroupFileTracked(e.workspace, e.group)
gf.Todos[e.taskIndex].Done = !gf.Todos[e.taskIndex].Done
// Aqui um safe save simplificado (read-modify-write atomico):
saveGroupFile(e.workspace, e.group, gf)
```
Neste caso o read acabou de acontecer, entao a janela de race condition e minima.

---

### FASE 4: Hot-Reload Automatico (File Watcher)
**Prioridade: MEDIA | Complexidade: MEDIA**

Detectar mudancas no diretorio de dados e recarregar automaticamente.

**4.1 - Dependencia:**

Adicionar `github.com/fsnotify/fsnotify` ao projeto:
```bash
go get github.com/fsnotify/fsnotify
```

**4.2 - Custom tea.Msg para notificar mudancas:**

```go
type fileChangedMsg struct {
    workspace string
    group     string // "" se for meta.json ou workspace-level
    path      string
}
```

**4.3 - Criar o watcher como tea.Cmd:**

```go
func watchDataDir() tea.Cmd {
    return func() tea.Msg {
        watcher, err := fsnotify.NewWatcher()
        if err != nil {
            return nil
        }

        dataDir := getDataDir()

        // Adicionar o diretorio principal e todos os subdiretorios (workspaces)
        _ = filepath.Walk(dataDir, func(path string, info os.FileInfo, err error) error {
            if err != nil {
                return nil
            }
            if info.IsDir() {
                _ = watcher.Add(path)
            }
            return nil
        })

        // Esperar pelo primeiro evento relevante
        for {
            select {
            case event := <-watcher.Events:
                if event.Op&(fsnotify.Write|fsnotify.Create) == 0 {
                    continue
                }
                if !strings.HasSuffix(event.Name, ".json") {
                    continue
                }

                // Debounce: esperar 500ms para o Nextcloud terminar de escrever
                time.Sleep(500 * time.Millisecond)

                // Parsear workspace e group do path
                rel, _ := filepath.Rel(dataDir, event.Name)
                parts := strings.Split(filepath.ToSlash(rel), "/")

                msg := fileChangedMsg{path: event.Name}
                if len(parts) >= 1 {
                    msg.workspace = parts[0]
                }
                if len(parts) >= 2 {
                    name := strings.TrimSuffix(parts[1], ".json")
                    if name != "meta" {
                        msg.group = name
                    }
                }

                watcher.Close()
                return msg

            case <-watcher.Errors:
                continue
            }
        }
    }
}
```

**4.4 - Processar a mensagem no Update():**

Adicionar novo case no `Update()` (linha ~549):

```go
case fileChangedMsg:
    // Ignorar mudancas causadas por nos mesmos (comparar hash)
    if msg.group != "" {
        key := msg.workspace + "/" + msg.group
        currentHash := fileHash(msg.path)
        if currentHash == m.fileHashes[key] {
            // Mudanca feita por nos, ignorar
            return m, watchDataDir() // re-registrar watcher
        }
    }

    // Recarregar dados conforme o estado atual
    switch {
    case msg.workspace != "" && msg.group != "" &&
         m.currentWorkspace == msg.workspace && m.currentGroup == msg.group:
        // Estamos vendo exatamente este grupo -> hot reload
        gf, hash := loadGroupFileTracked(msg.workspace, msg.group)
        key := msg.workspace + "/" + msg.group
        m.fileHashes[key] = hash
        m.lastKnownState[key] = deepCopyGroupFile(gf)
        m.tasks = gf
        // Ajustar cursor se necessario
        if m.taskCursor >= len(m.tasks.Todos) {
            m.taskCursor = max(0, len(m.tasks.Todos)-1)
        }

    case msg.group == "" && m.currentWorkspace == msg.workspace:
        // meta.json mudou -> recarregar lista de grupos
        m.groups, m.workspaceMeta = loadGroupsWithMeta(m.currentWorkspace)
        if m.groupCursor >= len(m.groups) {
            m.groupCursor = max(0, len(m.groups)-1)
        }

    case m.state == stateViewWorkspaces:
        // Workspace-level change -> recarregar lista
        m.workspaces = loadWorkspaces()
    }

    // Re-registrar o watcher para o proximo evento
    return m, watchDataDir()
```

**4.5 - Iniciar o watcher no Init():**

```go
func (m model) Init() tea.Cmd {
    return tea.Batch(tea.EnterAltScreen, watchDataDir())
}
```

**4.6 - Indicador visual de sync:**

Adicionar um indicador sutil quando uma mudanca externa e detectada.
Novo campo no model:

```go
syncNotice    string    // mensagem tipo "Updated by remote"
syncNoticeAt  time.Time // quando mostrar
```

Na view, se `time.Since(m.syncNoticeAt) < 3*time.Second`:
```go
syncStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#00CED1")).Faint(true)
s.WriteString(syncStyle.Render("  [synced]") + "\n")
```

---

### FASE 5: Merge do meta.json (Workspace Metadata)
**Prioridade: BAIXA | Complexidade: MEDIA**

O `meta.json` contem ordem dos grupos e favoritos. Conflitos aqui sao menos
criticos mas podem causar perda de ordem/favoritos.

**5.1 - Aplicar a mesma logica de hash + read-before-write:**

```go
func (m *model) safeSaveWorkspaceMeta(workspace string, meta WorkspaceMeta) {
    key := "meta/" + workspace
    diskData, _ := os.ReadFile(filepath.Join(getDataDir(), workspace, "meta.json"))
    diskHash := fmt.Sprintf("%x", md5.Sum(diskData))

    if diskHash == m.fileHashes[key] || m.fileHashes[key] == "" {
        // Sem mudanca externa, salvar direto
        saveWorkspaceMeta(workspace, meta)
        m.fileHashes[key] = fileHash(filepath.Join(getDataDir(), workspace, "meta.json"))
        return
    }

    // Merge: combinar favoritos de ambas versoes
    var diskMeta WorkspaceMeta
    _ = json.Unmarshal(diskData, &diskMeta)
    merged := mergeWorkspaceMeta(diskMeta, meta)
    saveWorkspaceMeta(workspace, merged)
    m.fileHashes[key] = fileHash(filepath.Join(getDataDir(), workspace, "meta.json"))
}
```

**5.2 - Merge de WorkspaceMeta:**

```go
func mergeWorkspaceMeta(disk, local WorkspaceMeta) WorkspaceMeta {
    // Unir grupos: manter ordem do local, adicionar novos do disco
    result := WorkspaceMeta{}
    seen := map[string]bool{}

    diskFavs := map[string]bool{}
    for _, gm := range disk.Groups {
        diskFavs[gm.Name] = gm.IsFavorite
    }

    for _, gm := range local.Groups {
        seen[gm.Name] = true
        // Se o disco marcou como favorito e local nao mudou, preservar
        if df, ok := diskFavs[gm.Name]; ok && df {
            gm.IsFavorite = gm.IsFavorite || df
        }
        result.Groups = append(result.Groups, gm)
    }

    // Adicionar grupos novos do disco
    for _, gm := range disk.Groups {
        if !seen[gm.Name] {
            result.Groups = append(result.Groups, gm)
        }
    }

    return result
}
```

---

## Resumo de Mudancas por Arquivo

### `main.go` (unico arquivo de codigo)

| Secao | Mudanca |
|-------|---------|
| imports | Adicionar `crypto/md5`, `path/filepath` (ja tem) |
| DATA STRUCTURES | Adicionar `AppConfig` struct |
| MODEL | Adicionar campos `fileHashes`, `lastKnownState`, `syncNotice`, `syncNoticeAt` |
| PERSISTENCE | Nova `loadAppConfig()`, alterar `getDataDir()`, criar `loadGroupFileTracked()`, `groupFilePath()`, `fileHash()` |
| MERGE | Novo bloco com `mergeGroupFiles()`, `mergeTodo()`, `mergeSubtasks()`, helpers de map |
| SYNC | Novo bloco com `safeSaveGroupFile()`, `safeSaveWorkspaceMeta()` |
| WATCHER | Novo bloco com `fileChangedMsg`, `watchDataDir()` |
| INIT | Alterar `Init()` para iniciar watcher |
| UPDATE | Adicionar case `fileChangedMsg` |
| HANDLERS | Substituir chamadas `saveGroupFile` por `safeSaveGroupFile` em ~15 locais |
| VIEW | Adicionar indicador `[synced]` |

### `go.mod`

Adicionar dependencia:
```
require github.com/fsnotify/fsnotify v1.7.0
```

### Novo: `config.json` (em %AppData%/todotui/)

```json
{
  "data_dir": ""
}
```

---

## Ordem de Implementacao Recomendada

```
FASE 1 (config data dir)
  |
  v
FASE 2 (file hashing)
  |
  v
FASE 3 (safe save + merge)   <-- mais critica, pode ser testada manualmente
  |
  v
FASE 4 (file watcher)         <-- hot reload automatico
  |
  v
FASE 5 (meta.json merge)      <-- polimento
```

**Estimativa de linhas de codigo novo:** ~250-300 linhas
**Linhas modificadas:** ~30-40 (substituicoes de chamadas)

---

## Limitacoes Conhecidas

1. **Titulo como chave primaria:** O merge usa `Todo.Title` como identificador unico.
   Se dois usuarios criarem tasks com o mesmo titulo, serao tratadas como a mesma task.
   - Solucao futura: adicionar campo `id string` (UUID) a cada Todo/Subtask

2. **Ordem de tasks:** Se ambos reordenam ao mesmo tempo, a ordem do usuario local prevalece.
   Nao ha merge sofisticado de ordenacao.

3. **Debounce do Nextcloud:** O delay de 500ms no watcher pode nao ser suficiente em
   conexoes lentas. Pode precisar ser configuravel.

4. **Rename de tasks:** Se um usuario renomeia uma task que o outro editou, o merge
   trata como uma delecao + criacao (perde associacao).
   - Solucao futura: campo `id` resolve isso tambem.

5. **Conflitos irreconciliaveis:** O merge sempre prioriza nao perder dados.
   Em caso de duvida, ambas as versoes sao mantidas.

---

## Testes Manuais Sugeridos

1. **Teste basico:** Usuario A adiciona task, arquivo sincroniza, Usuario B ve a task
2. **Teste de toggle:** A marca task como done, B ve a mudanca
3. **Teste de conflito:** A e B adicionam tasks diferentes ao mesmo grupo -> ambas aparecem
4. **Teste de merge:** A edita descricao, B marca como done -> ambas mudancas preservadas
5. **Teste de delete:** A deleta task, B edita a mesma task -> task mantida (seguro)
6. **Teste de hot-reload:** B edita enquanto A esta com o programa aberto -> A ve mudanca
