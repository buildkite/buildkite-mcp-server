import { execSync } from "child_process";
import { createInterface } from "readline";
import { createReadStream } from "fs";
import { writeFileSync } from "fs";

// ANSI color codes
const ColorReset = "\x1b[0m";
const ColorBold = "\x1b[1m";
const ColorDim = "\x1b[2m";
const ColorRed = "\x1b[31m";
const ColorGreen = "\x1b[32m";
const ColorYellow = "\x1b[33m";
const ColorBlue = "\x1b[34m";
const ColorMagenta = "\x1b[35m";
const ColorCyan = "\x1b[36m";
const ColorWhite = "\x1b[37m";
const ColorGray = "\x1b[90m";

let staticLineNum = 0;
let startTime: Date;

// Message interfaces
interface ContentItem {
    type: string;
    text?: string;
    id?: string;
    name?: string;
    input?: any;
    tool_use_id?: string;
    content?: any;
    is_error?: boolean;
}

interface Message {
    type: string;
    subtype?: string;
    message?: {
        id: string;
        type: string;
        role: string;
        model?: string;
        content: ContentItem[];
        usage?: {
            input_tokens: number;
            output_tokens: number;
        };
    };
    session_id?: string;
    tools?: string[];
    model?: string;
}

interface ChatEntry {
    lineNumber: number;
    speaker: string;
    content: string;
    timestamp: string;
    isJSON: boolean;
    rawLine: string;
}

// formatRelativeTime formats duration since start as MM:SS or HH:MM:SS
function formatRelativeTime(elapsed: number): string {
    const totalSeconds = Math.floor(elapsed / 1000);
    const hours = Math.floor(totalSeconds / 3600);
    const minutes = Math.floor((totalSeconds % 3600) / 60);
    const seconds = totalSeconds % 60;

    if (hours > 0) {
        return `${String(hours).padStart(2, "0")}:${String(minutes).padStart(2, "0")}:${String(seconds).padStart(2, "0")}`;
    }
    return `${String(minutes).padStart(2, "0")}:${String(seconds).padStart(2, "0")}`;
}

// createBuildkiteAnnotation creates a Buildkite annotation by parsing the raw JSON line
function createBuildkiteAnnotation(
    rawJSONLine: string,
    lineNumber: number,
    timestamp: string,
): void {
    // Skip empty lines
    rawJSONLine = rawJSONLine.trim();
    if (rawJSONLine === "") {
        return;
    }

    // Create markdown content for the annotation
    let markdownContent = "";
    let speaker = "";
    let content = "";
    let hasError = false;
    let style = "info"; // default style

    // Add timestamp and line number
    markdownContent += `**Message ${lineNumber}** - \`${timestamp}\`\n\n`;

    // Try to parse as JSON to extract clean content
    if (rawJSONLine.startsWith("{")) {
        try {
            const msg: Message = JSON.parse(rawJSONLine);
            // Extract clean content from JSON without ANSI codes and check for errors
            const result = extractCleanJSONContentWithErrorCheck(msg);
            speaker = result.speaker;
            content = result.content;
            hasError = result.hasError;

            // Skip annotation for unknown message types
            if (content === "Unknown message type") {
                return;
            }
        } catch (err) {
            // Not valid JSON, treat as plain text
            speaker = "SYSTEM";
            content = rawJSONLine;
        }
    } else {
        // Plain text line
        speaker = "SYSTEM";
        content = rawJSONLine;
    }

    // Add speaker with appropriate styling
    switch (speaker) {
        case "ASSISTANT":
            markdownContent += "🤖 **ASSISTANT**:\n\n";
            style = "info";
            break;
        case "USER":
            markdownContent += "👤 **USER**:\n\n";
            if (hasError) {
                style = "error";
            } else {
                style = "success";
            }
            break;
        case "SYSTEM":
            markdownContent += "⚙️ **SYSTEM**:\n\n";
            style = "warning";
            break;
        default:
            markdownContent += `**${speaker}**:\n\n`;
            style = "info";
    }

    // Add clean content (no ANSI codes)
    if (content !== "") {
        markdownContent += content;
    }

    // Add raw JSON disclosure at the end with pretty formatting
    markdownContent += "\n\n<details>\n<summary>Show JSON</summary>\n\n```json\n";

    // Pretty-format the JSON if possible
    if (rawJSONLine.startsWith("{")) {
        try {
            const jsonObj = JSON.parse(rawJSONLine);
            markdownContent += JSON.stringify(jsonObj, null, 2);
        } catch (err) {
            // Fallback to raw JSON if parsing fails
            markdownContent += rawJSONLine;
        }
    } else {
        // Not JSON, just show as-is
        markdownContent += rawJSONLine;
    }

    markdownContent += "\n```\n\n</details>";

    // Create context to ensure unique annotations
    const context = `chat-message-${lineNumber}`;

    // Execute buildkite-agent annotate command
    try {
        execSync(
            "buildkite-agent annotate --style " + style + " --context " + context + " --priority 5",
            {
                input: markdownContent,
                stdio: ["pipe", "inherit", "inherit"],
            },
        );
    } catch (err) {
        console.warn(`Warning: Failed to create Buildkite annotation: ${err}`);
    }
}

// extractCleanJSONContentWithErrorCheck extracts clean content from JSON message without ANSI codes and detects errors
function extractCleanJSONContentWithErrorCheck(msg: Message): {
    speaker: string;
    content: string;
    hasError: boolean;
} {
    switch (msg.type) {
        case "system":
            if (msg.subtype === "init") {
                return {
                    speaker: "SYSTEM",
                    content: `Session initialized (ID: ${msg.session_id}, Model: ${msg.model})`,
                    hasError: false,
                };
            }
            return { speaker: "SYSTEM", content: "System message", hasError: false };

        case "assistant": {
            const speaker = "ASSISTANT";
            let content = "";
            if (msg.message && msg.message.content.length > 0) {
                const contentParts: string[] = [];
                for (const contentItem of msg.message.content) {
                    switch (contentItem.type) {
                        case "text":
                            if (contentItem.text) {
                                contentParts.push(contentItem.text);
                            }
                            break;
                        case "tool_use": {
                            let toolInput = "";
                            if (contentItem.input) {
                                toolInput = JSON.stringify(contentItem.input, null, 2);
                            }

                            let toolDesc = `🔧 Using tool: ${contentItem.name}`;
                            if (toolInput && toolInput !== "{}") {
                                // Check if content needs progressive disclosure (multiple lines OR very long)
                                const lines = toolInput.split("\n");
                                const maxPreviewLength = 300;

                                const needsDisclosure =
                                    lines.length > 2 || toolInput.length > maxPreviewLength;

                                if (!needsDisclosure) {
                                    // Short input, show it all
                                    toolDesc += ` with ${toolInput}`;
                                } else {
                                    // Long input, show preview and put rest in disclosure
                                    let preview: string;
                                    let remaining: string;

                                    if (lines.length > 2) {
                                        // Multiple lines: show first 2 lines
                                        preview = lines.slice(0, 2).join("\n");
                                        remaining = lines.slice(2).join("\n");
                                    } else {
                                        // Single long line: truncate at reasonable length
                                        if (toolInput.length > maxPreviewLength) {
                                            preview = toolInput.slice(0, maxPreviewLength) + "...";
                                            remaining = toolInput.slice(maxPreviewLength);
                                        } else {
                                            preview = toolInput;
                                            remaining = "";
                                        }
                                    }

                                    if (remaining !== "") {
                                        // Use HTML details/summary for collapsible content
                                        toolDesc += ` with ${preview}\n\n<details>\n<summary>Show more input...</summary>\n\n\`\`\`json\n${remaining}\n\`\`\`\n\n</details>`;
                                    } else {
                                        toolDesc += ` with ${preview}`;
                                    }
                                }
                            }
                            contentParts.push(toolDesc);
                            break;
                        }
                    }
                }
                content = contentParts.join("\n\n");
            }
            return { speaker, content, hasError: false };
        }

        case "user": {
            const speaker = "USER";
            let hasError = false;
            let content = "";
            if (msg.message && msg.message.content.length > 0) {
                const contentParts: string[] = [];
                for (const contentItem of msg.message.content) {
                    if (contentItem.type === "tool_result") {
                        // Check for errors in tool results
                        if (contentItem.is_error) {
                            hasError = true;
                        }

                        // Extract and display the actual tool result content
                        let resultContent = "";
                        if (contentItem.text) {
                            resultContent = contentItem.text;
                            // Try to pretty-format if it's JSON
                            try {
                                const jsonObj = JSON.parse(resultContent);
                                resultContent = JSON.stringify(jsonObj, null, 2);
                            } catch (err) {
                                // Not JSON, keep as-is
                            }
                        } else if (contentItem.content) {
                            // Try to extract content from the Content field
                            resultContent = JSON.stringify(contentItem.content, null, 2);
                        }

                        if (resultContent !== "") {
                            const errorIndicator = contentItem.is_error
                                ? "❌ Tool error:"
                                : "✅ Tool result:";

                            // Check if content needs progressive disclosure (multiple lines OR very long)
                            const lines = resultContent.split("\n");
                            const maxPreviewLength = 400;

                            const needsDisclosure =
                                lines.length > 2 || resultContent.length > maxPreviewLength;

                            if (!needsDisclosure) {
                                // Short content, show it all
                                contentParts.push(errorIndicator + "\n" + resultContent);
                            } else {
                                // Long content, show preview and put rest in disclosure
                                let preview: string;
                                let remaining: string;

                                if (lines.length > 2) {
                                    // Multiple lines: show first 2 lines
                                    preview = lines.slice(0, 2).join("\n");
                                    remaining = lines.slice(2).join("\n");
                                } else {
                                    // Single long line: truncate at reasonable length
                                    if (resultContent.length > maxPreviewLength) {
                                        preview = resultContent.slice(0, maxPreviewLength) + "...";
                                        remaining = resultContent.slice(maxPreviewLength);
                                    } else {
                                        preview = resultContent;
                                        remaining = "";
                                    }
                                }

                                if (remaining !== "") {
                                    // Use HTML details/summary for collapsible content
                                    const disclosureContent = `${errorIndicator}\n${preview}\n\n<details>\n<summary>Show more...</summary>\n\n\`\`\`\n${remaining}\n\`\`\`\n\n</details>`;
                                    contentParts.push(disclosureContent);
                                } else {
                                    contentParts.push(errorIndicator + "\n" + preview);
                                }
                            }
                        } else {
                            contentParts.push("✅ Tool result received");
                        }
                    } else if (contentItem.text) {
                        contentParts.push(contentItem.text);
                    }
                }
                content = contentParts.join("\n\n");
            }
            return { speaker, content, hasError };
        }

        default:
            return {
                speaker: msg.type.toUpperCase(),
                content: "Unknown message type",
                hasError: false,
            };
    }
}

function formatJSONMessage(msg: Message): { speaker: string; content: string } {
    switch (msg.type) {
        case "system":
            if (msg.subtype === "init") {
                return {
                    speaker: "SYSTEM",
                    content: `Session initialized (ID: ${msg.session_id}, Model: ${msg.model})`,
                };
            }
            return { speaker: "SYSTEM", content: "System message" };

        case "assistant": {
            let speaker = "ASSISTANT";
            let content = "";
            if (msg.message && msg.message.content.length > 0) {
                const contentParts: string[] = [];
                for (const contentItem of msg.message.content) {
                    switch (contentItem.type) {
                        case "text":
                            if (contentItem.text) {
                                contentParts.push(contentItem.text);
                            }
                            break;
                        case "tool_use": {
                            let toolInput = "";
                            if (contentItem.input) {
                                toolInput = JSON.stringify(contentItem.input);
                            }
                            const withClause =
                                toolInput && toolInput !== "{}" ? " with " + toolInput : "";
                            contentParts.push(
                                `${ColorGreen}🔧 Using tool: ${contentItem.name}${withClause}${ColorReset}`,
                            );
                            break;
                        }
                    }
                }
                content = contentParts.join("\n");
            }
            return { speaker, content };
        }

        case "user": {
            let speaker = "USER";
            let content = "";
            if (msg.message && msg.message.content.length > 0) {
                const contentParts: string[] = [];
                for (const contentItem of msg.message.content) {
                    if (contentItem.type === "tool_result") {
                        // Extract and display the actual tool result content
                        let resultContent = "";
                        if (contentItem.text) {
                            resultContent = contentItem.text;
                            // Try to pretty-format if it's JSON
                            try {
                                const jsonObj = JSON.parse(resultContent);
                                resultContent = JSON.stringify(jsonObj, null, 2);
                            } catch (err) {
                                // Not JSON, keep as-is
                            }
                        } else if (contentItem.content) {
                            // Try to extract content from the Content field
                            resultContent = JSON.stringify(contentItem.content, null, 2);
                        }

                        if (resultContent !== "") {
                            const errorIndicator = contentItem.is_error
                                ? ColorRed + "❌ Tool error:" + ColorReset
                                : ColorMagenta + "✅ Tool result:" + ColorReset;
                            contentParts.push(errorIndicator + "\n" + resultContent);
                        } else {
                            contentParts.push(
                                ColorMagenta + "✅ Tool result received" + ColorReset,
                            );
                        }
                    } else if (contentItem.text) {
                        contentParts.push(contentItem.text);
                    }
                }
                content = contentParts.join("\n");
            }
            return { speaker, content };
        }

        default:
            return {
                speaker: msg.type.toUpperCase(),
                content: "Unknown message type",
            };
    }
}

function parseLine(line: string): ChatEntry | null {
    line = line.trim();
    if (line === "") {
        return null;
    }

    // Use a static line counter since the file doesn't have line numbers
    staticLineNum++;

    const entry: ChatEntry = {
        lineNumber: staticLineNum,
        rawLine: line,
        timestamp: formatRelativeTime(Date.now() - startTime.getTime()),
        speaker: "",
        content: "",
        isJSON: false,
    };

    const content = line;

    // Try to parse as JSON
    if (content.startsWith("{")) {
        try {
            const msg: Message = JSON.parse(content);
            entry.isJSON = true;
            const result = formatJSONMessage(msg);
            entry.speaker = result.speaker;
            entry.content = result.content;
        } catch (err) {
            // Not valid JSON, treat as plain text
            entry.speaker = "SYSTEM";
            entry.content = content;
        }
    } else {
        // Plain text line
        entry.speaker = "SYSTEM";
        entry.content = content;
    }

    return entry;
}

// printSingleEntry prints a single chat entry immediately (for streaming mode)
function printSingleEntry(entry: ChatEntry): void {
    if (entry.content === "") {
        return;
    }

    // Choose color based on speaker
    let speakerColor: string;
    let contentColor: string;
    switch (entry.speaker) {
        case "ASSISTANT":
            speakerColor = ColorGreen + ColorBold;
            contentColor = ColorGreen;
            break;
        case "USER":
            speakerColor = ColorBlue + ColorBold;
            contentColor = ColorBlue;
            break;
        case "SYSTEM":
            speakerColor = ColorYellow + ColorBold;
            contentColor = ColorGray;
            break;
        default:
            speakerColor = ColorWhite;
            contentColor = ColorWhite;
    }

    // Format: [LINE:123] [MM:SS] SPEAKER: content
    const prefix = `${ColorGray}[${String(entry.lineNumber).padStart(3, "0")}] ${ColorDim}[${entry.timestamp}]${ColorReset} ${speakerColor}${entry.speaker}:${ColorReset}`;

    // Handle multi-line content
    const lines = entry.content.split("\n");
    for (let i = 0; i < lines.length; i++) {
        if (i === 0) {
            console.log(`${prefix.padEnd(45)} ${contentColor}${lines[i]}${ColorReset}`);
        } else {
            console.log(`${contentColor}${lines[i]}${ColorReset}`);
        }
    }

    // Add spacing between messages for readability
    if (entry.isJSON) {
        console.log();
    }
}

function parseAndStreamOutput(rl: ReturnType<typeof createInterface>): Promise<void> {
    return new Promise((resolve, reject) => {
        rl.on("line", (line: string) => {
            const entry = parseLine(line);
            if (entry) {
                printSingleEntry(entry);
                createBuildkiteAnnotation(line, entry.lineNumber, entry.timestamp);
            }
        });

        rl.on("close", () => {
            resolve();
        });

        rl.on("error", err => {
            reject(err);
        });
    });
}

function parseAndStreamOutputWithFile(
    rl: ReturnType<typeof createInterface>,
    outputFilename: string,
): Promise<void> {
    return new Promise((resolve, reject) => {
        const lines: string[] = [];

        rl.on("line", (line: string) => {
            // Store raw JSON line to file later
            lines.push(line);

            // Parse and display the line
            const entry = parseLine(line);
            if (entry) {
                // Print to stdout with colors
                printSingleEntry(entry);
                // Create Buildkite annotation
                createBuildkiteAnnotation(line, entry.lineNumber, entry.timestamp);
            }
        });

        rl.on("close", () => {
            // Write all lines to file
            try {
                writeFileSync(outputFilename, lines.join("\n") + "\n");
                resolve();
            } catch (err) {
                reject(err);
            }
        });

        rl.on("error", err => {
            reject(err);
        });
    });
}

function printUsage(): void {
    console.log("Usage: chat-parser <input-file>");
    console.log("       cat <input-file> | chat-parser -");
    console.log("       cat <input-file> | chat-parser - -o <output-file>");
    console.log("");
    console.log("Options:");
    console.log("  -o <file>    Save output to file (only when streaming from stdin)");
}

async function main(): Promise<void> {
    // Initialize start time for relative timestamps
    startTime = new Date();

    let outputFile = "";
    let inputSource = "";

    // Parse command line arguments
    const args = process.argv.slice(2);
    if (args.length === 0) {
        printUsage();
        process.exit(1);
    }

    for (let i = 0; i < args.length; i++) {
        switch (args[i]) {
            case "-o":
                if (i + 1 >= args.length) {
                    console.log("Error: -o requires a filename");
                    printUsage();
                    process.exit(1);
                }
                outputFile = args[i + 1];
                i++; // Skip the next argument as it's the filename
                break;
            case "-":
                inputSource = "-";
                break;
            default:
                if (inputSource === "") {
                    inputSource = args[i];
                } else {
                    console.log(`Error: unexpected argument '${args[i]}'`);
                    printUsage();
                    process.exit(1);
                }
        }
    }

    if (inputSource === "") {
        printUsage();
        process.exit(1);
    }

    let rl: ReturnType<typeof createInterface>;
    let isStreaming = false;

    if (inputSource === "-") {
        // Read from stdin
        rl = createInterface({
            input: process.stdin,
            crlfDelay: Infinity,
        });
        isStreaming = true;
    } else {
        // Read from file
        rl = createInterface({
            input: createReadStream(inputSource),
            crlfDelay: Infinity,
        });
    }

    // Validate -o option usage
    if (outputFile !== "" && !isStreaming) {
        console.log("Error: -o option can only be used when streaming from stdin");
        process.exit(1);
    }

    // Print colorful header and process input line by line
    console.log(`${ColorCyan}${ColorBold}=== Claude Code Chat Transcript ===${ColorReset}`);
    console.log();

    try {
        if (outputFile !== "") {
            await parseAndStreamOutputWithFile(rl, outputFile);
        } else {
            await parseAndStreamOutput(rl);
        }
    } catch (err) {
        console.error(`Error: ${err}`);
        process.exit(1);
    }
}

// Run the main function
main().catch(error => {
    console.error("Error:", error.message);
    process.exit(1);
});
