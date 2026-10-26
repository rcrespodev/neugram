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
