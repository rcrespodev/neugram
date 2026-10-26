package transcript

import "testing"

func TestBashMutation(t *testing.T) {
	cases := []struct {
		name    string
		command string
		want    bool
	}{
		{"sed -i sobre fuente", "sed -i 's/a/b/' src/app.ts", true},
		{"sed -i sobre fuente absoluta", "sed -i 's/a/b/' /home/r/proj/src/a.ts", true},
		{"sed -i solo sobre scratchpad", "sed -i 's/a/b/' " + scratch, false},
		{"tee fuera de scratch", "tee /etc/x.conf < in", true},
		{"git commit", "git commit -m x", true},
		{"git push", "git push origin main", true},
		{"gh pr create", "gh pr create --fill", true},
		{"gh issue create", "gh issue create --title x", true},
		{"git status", "git status", false},
		{"heredoc a fuente", "cat > /home/r/proj/src/a.ts <<'X'\nhi\nX", true},
		{"append a fuente", "echo x >> /home/r/proj/src/a.ts", true},
		{"python heredoc redirigido a fuente", "python3 - <<'PY' > /home/r/proj/out.json\nprint(1)\nPY", true},
		{"redireccion a /dev/null", "make build > /dev/null 2>&1", false},
		{"solo redireccion de stderr", "ls 2>/dev/null", false},
		{"redireccion a scratchpad", "echo x > " + scratch, false},
		{"cuerpo de heredoc que cita una redireccion", "cat <<'X'\necho hi > /home/r/proj/src/a.ts\nX", false},
		{"heredoc a scratch que cita un sed -i", "cat > " + scratch + " <<'X'\nsed -i s/a/b/ /home/r/proj/src/a.ts\nX", false},
		{"2>&1 no es redireccion a archivo", "go test ./... 2>&1 | tail", false},
		{"mv a fuente", "mv a.txt src/b.txt", true},
		{"mv dentro de /tmp", "mv /tmp/a /tmp/b", false},
		{"rm en el repo", "rm -rf build/out", true},
		{"rm en /tmp", "rm -rf /tmp/x", false},
		{"mkdir", "mkdir -p internal/x", true},
		{"touch", "touch src/new.go", true},
		{"go mod tidy", "go mod tidy", true},
		{"npm i", "npm i left-pad", true},
		{"npm install", "npm install", true},
		{"ls no muta", "ls -la src", false},
		{"rm tras otro comando", "cd x && rm -f a.go", true},
		{"rm como argumento no es comando", "echo rm a.go", false},
		{"lectura con pipe", "cat a.go | grep x", false},
		{"git -C commit", "git -C /home/r/proj commit -m x", true},
		{"git -c commit", "git -c user.name=a commit -m x", true},
		{"git --git-dir push", "git --git-dir=/home/r/proj/.git push", true},
		{"git --no-pager commit", "git --no-pager commit -m x", true},
		{"git log -C no es commit", "git -C /home/r/proj log", false},
		{"commit citado en un echo", `echo "git commit"`, false},
		{"commit en el cuerpo de un heredoc", "cat <<'X'\ngit commit -m x\nX", false},
		{"flecha dentro de comillas", `codex exec "ineligible -> DEACTIVATE"`, false},
		{"flecha gorda en comillas simples", `echo 'x => y'`, false},
		{"awk con mayor que", `awk '$1>0' data.txt`, false},
		{"redireccion real tras comillas", `echo "a -> b" > out.txt`, true},
		{"redireccion &>", "make &> build.log", true},
		{"redireccion >|", "echo x >| out.txt", true},
		{"target con variable", "echo x > $WT/f", true},
		{"target con variable en /tmp", "echo x > /tmp/$f", false},
		{"rm con variable", "for f in a b; do rm src/$f.go; done", true},
		{"then mkdir", "if test -d x; then mkdir out; fi", true},
		{"llave antes del comando", "{ rm a.go; }", true},
		{"subshell", "(cd x && rm y)", true},
		{"time rm", "time rm a.go", true},
		{"xargs rm", "ls | xargs rm", true},
		{"xargs con flags rm", "find . -name x | xargs -I{} rm {}", true},
		{"sudo rm", "sudo rm -rf out", true},
		{"asignacion antes del comando", "FOO=1 rm a.go", true},
		{"committee no es tee", "echo committee ", false},
		{"tee a /dev/null", "echo x | tee /dev/null", false},
		{"tee a archivo", "echo x | tee out.txt", true},
		{"aritmetica no abre un heredoc", "echo $((1 << 2))\nrm src/a.go", true},
		{"<< entre comillas no abre un heredoc", "echo \"a << b\"\nrm src/a.go", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := scanSteps(t, Turn, bash(c.command))
			if res.Mutated() != c.want {
				t.Fatalf("muta = %v, quiero %v: %+v", res.Mutated(), c.want, res.Mutations)
			}
		})
	}
}

func TestBashMutationPaths(t *testing.T) {
	res := scanSteps(t, Turn, bash("echo x > out.txt && mv a.go /home/r/other/b.go"))
	got := map[string]bool{}
	for _, m := range res.Mutations {
		got[m.Path] = true
	}
	for _, want := range []string{"/home/r/proj/out.txt", "/home/r/other/b.go"} {
		if !got[want] {
			t.Errorf("falta %s en %+v", want, res.Mutations)
		}
	}
}

func TestBashFailedDoesNotMutate(t *testing.T) {
	if scanSteps(t, Turn, failed(bash("git commit -m x"))).Mutated() {
		t.Fatal("un commit fallido no muta")
	}
}

func TestBashPaths(t *testing.T) {
	cases := []struct {
		name       string
		command    string
		path       string
		unresolved bool
	}{
		{"cd antes de sed -i", "cd /home/r/repo/sub; sed -i s/a/b/ f.txt", "/home/r/repo/sub/f.txt", false},
		{"cd relativo", "cd sub && rm a.go", "/home/r/proj/sub/a.go", false},
		{"redireccion relativa", "echo x > out.txt", "/home/r/proj/out.txt", false},
		{"target con variable queda crudo", "echo x > $WT/f", "$WT/f", true},
		{"variable que no es scratch aunque la guarde", "echo x > $S/bench/run.sh", "$S/bench/run.sh", true},
		{"cd con variable deja el relativo", "cd $D && rm a.go", "a.go", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := scanSteps(t, Turn, bash(c.command))
			if len(res.Mutations) != 1 {
				t.Fatalf("%+v", res.Mutations)
			}
			if m := res.Mutations[0]; m.Path != c.path || m.Unresolved != c.unresolved {
				t.Fatalf("path=%q unresolved=%v, quiero %q %v", m.Path, m.Unresolved, c.path, c.unresolved)
			}
		})
	}
}
