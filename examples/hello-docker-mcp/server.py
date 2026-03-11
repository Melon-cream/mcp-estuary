import json
import sys


PROTOCOL_VERSION = "2025-06-18"
TOOL_NAME = "hello"


def send(message):
    sys.stdout.write(json.dumps(message) + "\n")
    sys.stdout.flush()


def response(message_id, result):
    return {"jsonrpc": "2.0", "id": message_id, "result": result}


def error(message_id, code, message):
    return {"jsonrpc": "2.0", "id": message_id, "error": {"code": code, "message": message}}


def handle_initialize(message_id):
    send(
        response(
            message_id,
            {
                "protocolVersion": PROTOCOL_VERSION,
                "capabilities": {"tools": {}},
                "serverInfo": {
                    "name": "hello-docker",
                    "title": "Hello Docker MCP",
                    "version": "0.1.0",
                },
            },
        )
    )


def handle_list_tools(message_id):
    send(
        response(
            message_id,
            {
                "tools": [
                    {
                        "name": TOOL_NAME,
                        "title": "hello",
                        "description": "Return a greeting from the hello-docker MCP example.",
                        "inputSchema": {
                            "type": "object",
                            "properties": {
                                "name": {
                                    "type": "string",
                                    "description": "Optional name to include in the greeting.",
                                }
                            },
                        },
                    }
                ]
            },
        )
    )


def handle_call_tool(message_id, params):
    if params.get("name") != TOOL_NAME:
        send(error(message_id, -32601, f"unknown tool {params.get('name')!r}"))
        return
    arguments = params.get("arguments") or {}
    name = arguments.get("name")
    message = "Hello from hello-docker."
    if isinstance(name, str) and name.strip():
        message = f"Hello, {name.strip()}, from hello-docker."
    send(
        response(
            message_id,
            {
                "content": [{"type": "text", "text": message}],
                "structuredContent": {"greeting": message},
            },
        )
    )


for raw_line in sys.stdin:
    line = raw_line.strip()
    if not line:
        continue
    try:
        message = json.loads(line)
    except json.JSONDecodeError:
        continue
    message_id = message.get("id")
    method = message.get("method")
    params = message.get("params") or {}
    if method == "initialize":
        handle_initialize(message_id)
    elif method == "notifications/initialized":
        continue
    elif method == "tools/list":
        handle_list_tools(message_id)
    elif method == "tools/call":
        handle_call_tool(message_id, params)
    elif method == "ping":
        send(response(message_id, {}))
    elif message_id is not None:
        send(error(message_id, -32601, f"unsupported method {method!r}"))
