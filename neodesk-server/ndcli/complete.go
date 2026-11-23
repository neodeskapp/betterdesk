package ndcli

import (
	"fmt"
	"sort"
	"strings"
)

// runCompletion prints the completion script of a shell. Every script
// asks `neodesk __complete <words>` for the candidates, so it never goes
// stale when commands are added.
func runCompletion(_ *Table, args []string, env *Env) int {
	if len(args) != 1 {
		writeMetaHelp(env.Stderr, "completion")
		return ExitUsage
	}
	script, ok := completionScripts[args[0]]
	if !ok {
		fmt.Fprintf(env.Stderr, "neodesk: no completion for %q; use bash, zsh, fish or powershell\n", args[0])
		return ExitUsage
	}
	fmt.Fprint(env.Stdout, script)
	return ExitOK
}

var completionScripts = map[string]string{
	"bash": `# neodesk completion for bash: source <(neodesk completion bash)
_neodesk() {
    local IFS=$'\n'
    COMPREPLY=($(neodesk __complete "${COMP_WORDS[@]:1:COMP_CWORD}" 2>/dev/null))
}
complete -o default -F _neodesk neodesk
`,
	"zsh": `#compdef neodesk
# neodesk completion for zsh: neodesk completion zsh > "${fpath[1]}/_neodesk"
_neodesk() {
    local -a candidates
    candidates=("${(@f)$(neodesk __complete "${(@)words[2,CURRENT]}" 2>/dev/null)}")
    compadd -a candidates
}
compdef _neodesk neodesk
`,
	"fish": `# neodesk completion for fish: neodesk completion fish > ~/.config/fish/completions/neodesk.fish
complete -c neodesk -f -a '(neodesk __complete (commandline -opc)[2..-1] (commandline -ct) 2>/dev/null)'
`,
	"powershell": `# neodesk completion for PowerShell: neodesk completion powershell | Out-String | Invoke-Expression
Register-ArgumentCompleter -Native -CommandName neodesk -ScriptBlock {
    param($wordToComplete, $commandAst, $cursorPosition)
    $words = @($commandAst.CommandElements | Select-Object -Skip 1 | ForEach-Object { $_.ToString() })
    if ($wordToComplete -eq '') { $words += '' }
    neodesk __complete @words 2>$null | ForEach-Object {
        [System.Management.Automation.CompletionResult]::new($_, $_, 'ParameterValue', $_)
    }
}
`,
}

// runComplete prints the candidates for the last of the words.
func runComplete(t *Table, words []string, env *Env) int {
	for _, c := range completions(t, words) {
		fmt.Fprintln(env.Stdout, c)
	}
	return ExitOK
}

// completions lists what fits the last word, given the words before it.
func completions(t *Table, words []string) []string {
	if len(words) == 0 {
		words = []string{""}
	}
	current := words[len(words)-1]
	before := words[:len(words)-1]
	var plain []string
	for _, w := range before {
		if !strings.HasPrefix(w, "-") {
			plain = append(plain, w)
		}
	}
	var out []string
	add := func(candidates ...string) {
		for _, c := range candidates {
			if strings.HasPrefix(c, current) {
				out = append(out, c)
			}
		}
	}
	global := []string{"--json", "--output", "--url", "--token-file", "--yes", "--verbose", "--help", "--insecure", "--ca-cert", "--allow-http", "--timeout", "--data", "-q", "-f", "-F"}
	switch {
	case len(plain) == 0:
		if strings.HasPrefix(current, "-") {
			add(global...)
			break
		}
		add(groupNames(t)...)
		add(metaNames()...)
	case len(plain) == 1:
		if t.HasGroup(plain[0]) {
			if strings.HasPrefix(current, "-") {
				add("--help")
				break
			}
			for _, c := range t.InGroup(plain[0]) {
				add(c.Action())
			}
		} else if plain[0] == "completion" {
			add("bash", "fish", "powershell", "zsh")
		} else if plain[0] == "help" {
			add(groupNames(t)...)
		} else if strings.HasPrefix(current, "-") {
			add(global...)
		}
	default:
		cmd := t.Find(plain[0], plain[1])
		if cmd == nil {
			if plain[0] == "help" && t.HasGroup(plain[1]) {
				for _, c := range t.InGroup(plain[1]) {
					add(c.Action())
				}
			}
			break
		}
		if strings.HasPrefix(current, "-") {
			for _, f := range cmd.Flags {
				add("--" + f.Name)
				if f.Secret {
					add("--" + f.Name + "-stdin")
				}
			}
			if cmd.Upload != "" {
				add("--file")
			}
			add(global...)
		}
		// Values of an enum flag
		if len(before) > 0 {
			last := strings.TrimLeft(before[len(before)-1], "-")
			for _, f := range cmd.Flags {
				if f.Name == last && len(f.Enum) > 0 {
					out = nil
					add(f.Enum...)
				}
			}
		}
	}
	sort.Strings(out)
	return out
}
