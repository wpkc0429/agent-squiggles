#!/usr/bin/env python3
"""Runs the agent-squiggles evaluation.

Each task is a small repository with an AGENTS.md that tells Codex how to
build and test it, plus a change request whose callers are easy to miss with
grep alone. Every task is run by `codex exec` twice per repetition: once
without hooks (the "baseline": Codex relies on the checker named in
AGENTS.md) and once with the agent-squiggles hooks installed. Nothing else
differs between the two conditions.

Metrics come from Codex's JSON event stream and from re-running the task's
check command afterwards. See eval/README.md for the method and caveats.

Requires Python 3.8+, git, the codex CLI logged in, and the toolchains the
tasks need (go, node, python3, cargo).
"""
import argparse
import datetime
import json
import os
import re
import shutil
import stat
import subprocess
import sys
import tempfile
import time

HERE = os.path.dirname(os.path.abspath(__file__))
TASKS_DIR = os.path.join(HERE, "tasks")
REPO_ROOT = os.path.dirname(HERE)
CONDITIONS = ("baseline", "squiggles")

# Commands that build, type-check or test the project.
CHECKER_RE = re.compile(
    r"\b(go\s+(build|vet|test)\b|tsc\b|npm\s+(run\s+)?(test|build|typecheck|check)\b"
    r"|pyright\b|mypy\b|python3?\s+-m\s+(unittest|pytest|py_compile|compileall)\b"
    r"|pytest\b|cargo\s+(check|build|test|clippy)\b)"
)
LIMIT_RE = re.compile(r"usage limit|rate limit|quota", re.I)


def log(msg):
    print("[eval %s] %s" % (time.strftime("%H:%M:%S"), msg), flush=True)


def load_tasks(names):
    all_ids = sorted(d for d in os.listdir(TASKS_DIR) if os.path.isfile(os.path.join(TASKS_DIR, d, "task.json")))
    ids = all_ids if names == "all" else names.split(",")
    tasks = []
    for tid in ids:
        if tid not in all_ids:
            sys.exit("unknown task %r (have: %s)" % (tid, ", ".join(all_ids)))
        with open(os.path.join(TASKS_DIR, tid, "task.json")) as f:
            t = json.load(f)
        t["id"] = tid
        tasks.append(t)
    return tasks


def git(repo, *args):
    env = dict(os.environ, GIT_AUTHOR_NAME="eval", GIT_AUTHOR_EMAIL="eval@example.com",
               GIT_COMMITTER_NAME="eval", GIT_COMMITTER_EMAIL="eval@example.com")
    subprocess.run(["git"] + list(args), cwd=repo, env=env, check=True,
                   stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


def build_binary(out_dir):
    """Builds the checked-out agent-squiggles and returns (path, version)."""
    path = os.path.join(out_dir, "agent-squiggles")
    subprocess.run(["go", "build", "-o", path, "./cmd/agent-squiggles"], cwd=REPO_ROOT, check=True)
    rev = subprocess.run(["git", "describe", "--tags", "--always", "--dirty"], cwd=REPO_ROOT,
                         stdout=subprocess.PIPE, check=True).stdout.decode().strip()
    return path, rev


def prepare_repo(task, run_dir, ts_modules):
    repo = os.path.join(run_dir, "repo")
    shutil.copytree(os.path.join(TASKS_DIR, task["id"], "repo"), repo)
    if task.get("setup", {}).get("node_modules"):
        os.symlink(ts_modules, os.path.join(repo, "node_modules"))
    git(repo, "init", "-q")
    git(repo, "add", "-A")
    git(repo, "commit", "-q", "-m", "task")
    return repo


def make_codex_home(home, repo, args, hooks_bin):
    """Creates a throwaway CODEX_HOME. The auth file is copied in and must be
    removed by the caller (see remove_auth)."""
    os.makedirs(home)
    with open(os.path.join(home, "config.toml"), "w") as f:
        f.write('model = "%s"\nmodel_reasoning_effort = "%s"\n\n' % (args.model, args.effort))
        f.write('[projects."%s"]\ntrust_level = "trusted"\n' % repo)
    auth = os.path.join(home, "auth.json")
    shutil.copyfile(os.path.expanduser(args.auth), auth)
    os.chmod(auth, stat.S_IRUSR | stat.S_IWUSR)
    if hooks_bin:
        subprocess.run([hooks_bin, "install", "--hooks-file", os.path.join(home, "hooks.json"), "--no-servers"],
                       check=True, stdout=subprocess.DEVNULL)


def remove_auth(home):
    try:
        os.remove(os.path.join(home, "auth.json"))
    except FileNotFoundError:
        pass


def task_env(run_dir, extra_path):
    env = dict(os.environ)
    # Codex's workspace-write sandbox only allows writes inside the workspace
    # and temp dirs, so keep tool caches there.
    env["GOCACHE"] = os.path.join(run_dir, "gocache")
    env["npm_config_cache"] = os.path.join(run_dir, "npm-cache")
    if extra_path:
        env["PATH"] = extra_path + os.pathsep + env["PATH"]
    return env


def run_codex(home, repo, prompt, squiggles, env, timeout):
    cmd = ["codex", "exec", "--json", "-s", "workspace-write", "-C", repo]
    if squiggles:
        # The throwaway CODEX_HOME cannot have its hooks reviewed interactively.
        cmd.insert(2, "--dangerously-bypass-hook-trust")
    cmd.append(prompt)
    env = dict(env, CODEX_HOME=home)
    start = time.time()
    try:
        p = subprocess.run(cmd, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                           env=env, timeout=timeout)
        code, out, err = p.returncode, p.stdout.decode("utf-8", "replace"), p.stderr.decode("utf-8", "replace")
    except subprocess.TimeoutExpired as e:
        code = "timeout"
        out = (e.stdout or b"").decode("utf-8", "replace")
        err = (e.stderr or b"").decode("utf-8", "replace")
    return code, out, err, time.time() - start


def parse_events(text):
    events = []
    for line in text.splitlines():
        line = line.strip()
        if line.startswith("{"):
            try:
                events.append(json.loads(line))
            except ValueError:
                pass
    return events


def metrics(events):
    m = {"commands": 0, "checker_runs": 0, "checker_failures": 0, "first_checker_passed": None,
         "edits": 0, "files_edited": 0, "agent_messages": 0,
         "input_tokens": 0, "cached_input_tokens": 0, "output_tokens": 0, "reasoning_tokens": 0,
         "errors": []}
    files = set()
    for ev in events:
        typ = ev.get("type")
        item = ev.get("item") or {}
        if typ == "item.completed":
            kind = item.get("type")
            if kind == "command_execution":
                m["commands"] += 1
                if CHECKER_RE.search(item.get("command", "")):
                    m["checker_runs"] += 1
                    ok = item.get("exit_code") == 0
                    if not ok:
                        m["checker_failures"] += 1
                    if m["first_checker_passed"] is None:
                        m["first_checker_passed"] = ok
            elif kind == "file_change":
                m["edits"] += 1
                for c in item.get("changes") or []:
                    files.add(c.get("path"))
            elif kind == "agent_message":
                m["agent_messages"] += 1
                m["final_message"] = item.get("text", "")[:600]
            elif kind == "error":
                msg = item.get("message", "")
                if "--dangerously-bypass-hook-trust" not in msg:  # expected warning, see run_codex
                    m["errors"].append(msg[:300])
        elif typ == "turn.completed":
            u = ev.get("usage") or {}
            m["input_tokens"] += u.get("input_tokens", 0)
            m["cached_input_tokens"] += u.get("cached_input_tokens", 0)
            m["output_tokens"] += u.get("output_tokens", 0)
            m["reasoning_tokens"] += u.get("reasoning_output_tokens", 0)
        elif typ in ("turn.failed", "error"):
            m["errors"].append(json.dumps(ev)[:300])
    m["files_edited"] = len(files)
    return m


def run_check(task, repo, env, timeout):
    try:
        p = subprocess.run(["sh", "-c", task["check"]], cwd=repo, env=env, stdout=subprocess.PIPE,
                           stderr=subprocess.STDOUT, timeout=timeout)
        return p.returncode == 0, p.stdout.decode("utf-8", "replace")[-1500:]
    except subprocess.TimeoutExpired:
        return False, "check timed out"


def squiggles_activity(repo):
    """Counts the reports agent-squiggles gave Codex, from its daemon log."""
    data = os.environ.get("AGENT_SQUIGGLES_HOME") or os.path.join(
        os.environ.get("XDG_DATA_HOME") or os.path.expanduser("~/.local/share"), "agent-squiggles")
    logs = os.path.join(data, "logs")
    out = {"checks": 0, "reports": 0, "new_errors": 0}
    if not os.path.isdir(logs):
        return out
    marker = "daemon starting for %s " % repo
    for name in os.listdir(logs):
        path = os.path.join(logs, name)
        with open(path, errors="replace") as f:
            text = f.read()
        if marker not in text:
            continue
        for line in text[text.rindex(marker):].splitlines():
            mt = re.search(r" post \S+ \S+: (\d+) new", line)
            if mt:
                out["checks"] += 1
                n = int(mt.group(1))
                if n:
                    out["reports"] += 1
                    out["new_errors"] += n
    return out


def run_one(task, cond, rep, args, bin_path, version):
    run_id = "%s-%s-%d" % (task["id"], cond, rep)
    run_dir = tempfile.mkdtemp(prefix=run_id + "-", dir=args.workdir)
    repo = prepare_repo(task, run_dir, os.path.expanduser(args.ts_modules))
    home = os.path.join(run_dir, "codex-home")
    squiggles = cond == "squiggles"
    env = task_env(run_dir, args.path_prepend)
    try:
        make_codex_home(home, repo, args, bin_path if squiggles else None)
        code, out, err, secs = run_codex(home, repo, task["prompt"], squiggles, env, args.timeout)
    finally:
        remove_auth(home)
        if squiggles:
            subprocess.run([bin_path, "stop"], cwd=repo, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    events = parse_events(out)
    with open(os.path.join(args.out, "transcripts", run_id + ".jsonl"), "w") as f:
        f.write(out)
    ok, check_out = run_check(task, repo, env, args.timeout)
    m = metrics(events)
    rec = {
        "run": run_id, "task": task["id"], "language": task["language"], "condition": cond, "rep": rep,
        "model": args.model, "effort": args.effort, "agent_squiggles": version,
        "codex_exit": code, "seconds": round(secs, 1),
        "success": ok, "check_tail": check_out if not ok else "",
    }
    rec.update(m)
    if squiggles:
        rec["squiggles"] = squiggles_activity(repo)
    limited = bool(LIMIT_RE.search(err)) or any(LIMIT_RE.search(e) for e in m["errors"])
    rec["limited"] = limited
    rec["valid"] = code == 0 and not limited and m["agent_messages"] > 0
    return rec


def summarize(out_dir):
    path = os.path.join(out_dir, "runs.jsonl")
    with open(path) as f:
        runs = [json.loads(l) for l in f if l.strip()]
    # excluded.json maps run IDs to the reason they are left out of the
    # tables (for example a broken toolchain). runs.jsonl is never edited.
    excluded = {}
    ex_path = os.path.join(out_dir, "excluded.json")
    if os.path.exists(ex_path):
        with open(ex_path) as f:
            excluded = json.load(f)
    valid = [r for r in runs if r.get("valid") and r["run"] not in excluded]

    def mean(xs):
        xs = [x for x in xs if x is not None]
        return sum(xs) / len(xs) if xs else float("nan")

    def row(label, rs):
        firsts = [r["first_checker_passed"] for r in rs if r["first_checker_passed"] is not None]
        return "| %s | %d | %d/%d | %.1f | %.1f | %s | %.1f | %.0fk / %.1fk | %.0f s |" % (
            label, len(rs), sum(r["success"] for r in rs), len(rs),
            mean([r["checker_runs"] for r in rs]), mean([r["checker_failures"] for r in rs]),
            ("%d/%d" % (sum(firsts), len(firsts))) if firsts else "–",
            mean([r["edits"] for r in rs]),
            mean([r["input_tokens"] for r in rs]) / 1000, mean([r["output_tokens"] for r in rs]) / 1000,
            mean([r["seconds"] for r in rs]))

    header = ("| Condition | Runs | Success | Checker runs | Failed checker runs | First check passed "
              "| Edits | Tokens in / out | Time |\n|---|---|---|---|---|---|---|---|---|")
    lines = ["# Results", "",
             "Generated by `eval/run.py` from `runs.jsonl` (%d valid runs of %d)." % (len(valid), len(runs)),
             "Model: %s. agent-squiggles: %s." % (
                 ", ".join(sorted({"%s (%s)" % (r["model"], r["effort"]) for r in runs})),
                 ", ".join(sorted({r.get("agent_squiggles", "unknown") for r in runs}))), "",
             "## Overall", "", header]
    for cond in CONDITIONS:
        rs = [r for r in valid if r["condition"] == cond]
        if rs:
            lines.append(row(cond, rs))
    lines += ["", "## By task", "", header]
    for tid in sorted({r["task"] for r in valid}):
        for cond in CONDITIONS:
            rs = [r for r in valid if r["task"] == tid and r["condition"] == cond]
            if rs:
                lines.append(row("%s / %s" % (tid, cond), rs))
    sq = [r for r in valid if r["condition"] == "squiggles" and r.get("squiggles")]
    if sq:
        lines += ["", "agent-squiggles ran %d checks in these runs and reported new errors %d times "
                  "(%d errors in total)." % (sum(r["squiggles"]["checks"] for r in sq),
                                             sum(r["squiggles"]["reports"] for r in sq),
                                             sum(r["squiggles"]["new_errors"] for r in sq))]
    invalid = [r for r in runs if not r.get("valid")]
    if invalid or excluded:
        lines += ["", "Excluded runs:", ""]
        lines += ["- %s: Codex exited with %s%s" % (r["run"], r["codex_exit"], ", usage limit" if r.get("limited") else "")
                  for r in invalid]
        lines += ["- %s: %s" % (run, reason) for run, reason in sorted(excluded.items())]
    lines += ["", "Columns: *Checker runs* counts the build, type-check and test commands Codex ran "
              "(see `CHECKER_RE` in run.py); *Failed checker runs* is how many of them failed, i.e. edit, "
              "build, fix loops; *First check passed* is how often the first such command already "
              "succeeded. *Success* re-runs the task's check after Codex finishes.", ""]
    with open(os.path.join(out_dir, "summary.md"), "w") as f:
        f.write("\n".join(lines))
    return "\n".join(lines)


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--tasks", default="all", help="comma-separated task IDs, or all")
    ap.add_argument("--reps", type=int, default=2, help="repetitions per task and condition")
    ap.add_argument("--rep-start", type=int, default=1, help="number of the first repetition (to add runs to a batch)")
    ap.add_argument("--model", default="gpt-5.6-luna")
    ap.add_argument("--effort", default="medium")
    ap.add_argument("--auth", default="~/.codex/auth.json", help="Codex auth file to copy into each run")
    ap.add_argument("--out", default=os.path.join(HERE, "results", datetime.date.today().isoformat()))
    ap.add_argument("--workdir", default=None, help="where run directories go (default: a temp dir)")
    ap.add_argument("--ts-modules", default="~/.local/share/agent-squiggles/servers/typescript/node_modules",
                    help="node_modules with TypeScript, linked into TypeScript tasks")
    ap.add_argument("--path-prepend", default="", help="extra PATH entries (for example a cargo bin dir)")
    ap.add_argument("--timeout", type=int, default=600, help="seconds per Codex run and per check")
    ap.add_argument("--summarize", action="store_true", help="only rebuild summary.md from runs.jsonl")
    args = ap.parse_args()

    if args.summarize:
        print(summarize(args.out))
        return
    os.makedirs(os.path.join(args.out, "transcripts"), exist_ok=True)
    args.workdir = args.workdir or tempfile.mkdtemp(prefix="agent-squiggles-eval-")
    os.makedirs(args.workdir, exist_ok=True)
    tasks = load_tasks(args.tasks)
    bin_path, version = build_binary(args.workdir)
    log("agent-squiggles %s, %d tasks x %d conditions x %d reps, runs in %s" % (
        version, len(tasks), 2, args.reps, args.workdir))

    with open(os.path.join(args.out, "runs.jsonl"), "a") as results:
        for rep in range(args.rep_start, args.rep_start + args.reps):
            for i, task in enumerate(tasks):
                # Alternate which condition goes first to spread out time-of-day effects.
                order = CONDITIONS if (rep + i) % 2 else tuple(reversed(CONDITIONS))
                for cond in order:
                    log("%s / %s / rep %d" % (task["id"], cond, rep))
                    rec = run_one(task, cond, rep, args, bin_path, version)
                    results.write(json.dumps(rec) + "\n")
                    results.flush()
                    log("  success=%s checker_runs=%s failures=%s edits=%s %.0fs%s" % (
                        rec["success"], rec["checker_runs"], rec["checker_failures"], rec["edits"],
                        rec["seconds"], "" if rec["valid"] else " (invalid: exit %s)" % rec["codex_exit"]))
                    if rec["limited"]:
                        log("usage limit reached; stopping")
                        print(summarize(args.out))
                        return
    print(summarize(args.out))


if __name__ == "__main__":
    main()
