// Read JSON-encoded shell command strings on stdin; emit a command -> bk calls
// lookup for the jq transcript reports. Parse only: never execute agent code.
import { readFileSync } from "node:fs";
import { basename } from "node:path";
import { spawnSync } from "node:child_process";

function literal(node, quoted = false) {
    if (node.Type === "Lit") {
        return node.Value.replace(
            quoted ? /\\([$`"\\\n])/g : /\\(.)/gs,
            (_, char) => (char === "\n" ? "" : char),
        );
    }
    if (node.Type === "SglQuoted" && !node.Dollar) return node.Value;
    if (node.Parts) {
        const parts = node.Parts.map((part) =>
            literal(part, node.Type === "DblQuoted"),
        );
        if (parts.every((part) => part !== null)) return parts.join("");
    }
    return null; // Expansions are not statically known command words.
}

function bkCommand(words) {
    let i = 0;
    while (words[i] !== null && i < words.length) {
        const name = basename(words[i++]);
        if (name === "bk") {
            const command = ["bk"];
            while (
                command.length < 3 &&
                /^[a-z][a-z-]*$/.test(words[i] ?? "")
            ) {
                command.push(words[i++]);
            }
            return command.join(" ");
        }
        if (!["env", "sudo", "command", "exec"].includes(name)) return null;
        // Recognize wrapper options explicitly: treating an option's value as
        // a program would turn e.g. `env -u bk echo hi` into a false bk call.
        while (typeof words[i] === "string" && words[i].startsWith("-")) {
            const option = words[i++];
            if (option === "--") break;
            const operands = {
                env: ["-u", "--unset", "-C", "--chdir"],
                sudo: ["-u", "--user", "-g", "--group"],
                exec: ["-a"],
                command: [],
            };
            const flags = {
                env: /^(-i|--ignore-environment|--unset=.+|--chdir=.+)$/,
                sudo: /^(-[nEHSk]+|--non-interactive|--preserve-env(?:=.+)?|--user=.+|--group=.+)$/,
                exec: /^-[cl]+$/,
                command: /^-p$/,
            };
            if (operands[name].includes(option)) i++;
            else if (!flags[name].test(option)) return null;
        }
        if (name === "env") {
            while (/^[A-Za-z_][A-Za-z0-9_]*=/.test(words[i] ?? "")) i++;
        }
    }
    return null;
}

function calls(command) {
    const parsed = spawnSync("shfmt", ["-ln", "bash", "-tojson"], {
        input: command,
        encoding: "utf8",
        maxBuffer: 32 * 1024 * 1024,
    });
    if (parsed.error) throw parsed.error;
    if (parsed.status !== 0) {
        console.error(
            "bk shell audit: invalid Bash syntax in transcript; CLI counts unavailable",
        );
        return null;
    }
    const found = [];
    function visit(node) {
        if (!node || typeof node !== "object") return;
        if (node.Type === "CallExpr") {
            const name = bkCommand(
                (node.Args ?? []).map((word) => literal(word)),
            );
            if (name) found.push({ offset: node.Pos.Offset, name });
        }
        // Quoted text and heredoc bodies contain literal nodes, not CallExprs.
        // Real command substitutions (including unquoted heredocs) do contain
        // CallExprs and must still be visited.
        for (const value of Object.values(node)) visit(value);
    }
    visit(JSON.parse(parsed.stdout));
    return found.sort((a, b) => a.offset - b.offset).map((call) => call.name);
}

try {
    const commands = readFileSync(0, "utf8")
        .split("\n")
        .filter(Boolean)
        .map((line) => JSON.parse(line));
    process.stdout.write(
        JSON.stringify(
            Object.fromEntries(
                [...new Set(commands)].map((command) => [
                    command,
                    calls(command),
                ]),
            ),
        ),
    );
} catch (error) {
    console.error(`bk shell audit: ${error.message}`);
    process.exitCode = 1;
}
