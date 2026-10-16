// The code examples of the Documentation pages, one per language, each runnable
// against a real gateway. Comments are the only translated part: pass the English
// or the Spanish set from `comments`.
//
// No example uses a dollar-brace, so the sources can stay plain template literals
// (the Go ones escape their backticks).

export const comments = {
  en: {
    token: "the gateway token: motita gateway start -token-only",
    welcome: "the server greets first; reply with the token",
    ask: "authenticated: now ask something",
    beat: "answer the keep-alive within 10 s",
    done: "the final answer",
    fail: "an error, or another client is already running a task",
    wrong: "wrong token (the connection stays open)",
    create: "create a plan that runs every 24 hours",
    show: "the schedule, as stored",
  },
  es: {
    token: "el token del gateway: motita gateway start -token-only",
    welcome: "el servidor saluda primero; responde con el token",
    ask: "autenticado: ahora pregunta algo",
    beat: "responde el keep-alive en menos de 10 s",
    done: "la respuesta final",
    fail: "un error, o otro cliente ya está ejecutando una tarea",
    wrong: "token incorrecto (la conexión sigue abierta)",
    create: "crea un plan que se ejecuta cada 24 horas",
    show: "la tarea programada, tal como queda guardada",
  },
};

const WS_URL = "ws://127.0.0.1:7477/v1/sessions/default/ws";
const QUESTION = "what does this project do?";

function wsJs(c) {
  return `// npm install ws
import WebSocket from "ws";
import { randomUUID } from "node:crypto";

const TOKEN = process.env.MOTITA_TOKEN; // ${c.token}
const ws = new WebSocket("${WS_URL}", {
  headers: { Authorization: "Bearer " + TOKEN },
});

const send = (type, payload) =>
  ws.send(JSON.stringify({
    msg_id: randomUUID(), type, flags: [],
    timestamp: new Date().toISOString(), payload,
  }));

ws.on("open", () => send("auth", { token: TOKEN })); // ${c.welcome}

ws.on("message", (data) => {
  const msg = JSON.parse(data.toString());
  const has = (flag) => msg.flags.includes(flag);

  if (msg.type === "auth_response" && has("AUTHENTICATED")) {
    send("query", { query: "${QUESTION}" }); // ${c.ask}
  } else if (msg.type === "auth_response" && has("UNAUTHORIZED")) {
    console.error("unauthorized"); // ${c.wrong}
    ws.close();
  } else if (msg.type === "heartbeat") {
    send("heartbeat_ack", {}); // ${c.beat}
  } else if (has("COMPLETED")) {
    console.log(msg.payload.result); // ${c.done}
    ws.close();
  } else if (msg.type === "error" || has("BUSY")) {
    console.error(msg.payload); // ${c.fail}
    ws.close();
  }
});`;
}

function wsTs(c) {
  return `// npm install ws @types/ws
import WebSocket from "ws";
import { randomUUID } from "node:crypto";

interface Envelope<P = Record<string, unknown>> {
  msg_id: string;
  type: string;
  flags: string[];
  timestamp: string;
  payload: P;
}

const TOKEN = process.env.MOTITA_TOKEN ?? ""; // ${c.token}
const ws = new WebSocket("${WS_URL}", {
  headers: { Authorization: "Bearer " + TOKEN },
});

function send(type: string, payload: object): void {
  const msg: Envelope<object> = {
    msg_id: randomUUID(), type, flags: [],
    timestamp: new Date().toISOString(), payload,
  };
  ws.send(JSON.stringify(msg));
}

ws.on("open", () => send("auth", { token: TOKEN })); // ${c.welcome}

ws.on("message", (data) => {
  const msg = JSON.parse(data.toString()) as Envelope<{ result?: string }>;
  const has = (flag: string): boolean => msg.flags.includes(flag);

  if (msg.type === "auth_response" && has("AUTHENTICATED")) {
    send("query", { query: "${QUESTION}" }); // ${c.ask}
  } else if (msg.type === "heartbeat") {
    send("heartbeat_ack", {}); // ${c.beat}
  } else if (has("COMPLETED")) {
    console.log(msg.payload.result); // ${c.done}
    ws.close();
  } else if (msg.type === "error" || has("BUSY") || has("UNAUTHORIZED")) {
    console.error(msg.payload); // ${c.fail}
    ws.close();
  }
});`;
}

function wsPy(c) {
  return `# pip install websocket-client
import datetime
import json
import os
import uuid

import websocket

TOKEN = os.environ["MOTITA_TOKEN"]  # ${c.token}


def send(ws, type_, payload):
    ws.send(json.dumps({
        "msg_id": str(uuid.uuid4()), "type": type_, "flags": [],
        "timestamp": datetime.datetime.now(datetime.timezone.utc).isoformat(),
        "payload": payload,
    }))


ws = websocket.create_connection(
    "${WS_URL}",
    header=["Authorization: Bearer " + TOKEN],
)
json.loads(ws.recv())  # ${c.welcome}
send(ws, "auth", {"token": TOKEN})
json.loads(ws.recv())  # auth_response / AUTHENTICATED
send(ws, "query", {"query": "${QUESTION}"})  # ${c.ask}

while True:
    msg = json.loads(ws.recv())
    if msg["type"] == "heartbeat":
        send(ws, "heartbeat_ack", {})  # ${c.beat}
    elif "COMPLETED" in msg["flags"]:
        print(msg["payload"]["result"])  # ${c.done}
        break
    elif msg["type"] == "error" or "BUSY" in msg["flags"]:
        raise SystemExit(msg["payload"])  # ${c.fail}`;
}

function wsGo(c) {
  return `// go get github.com/gorilla/websocket github.com/google/uuid
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

type Envelope struct {
	MsgID     string          \`json:"msg_id"\`
	Type      string          \`json:"type"\`
	Flags     []string        \`json:"flags"\`
	Timestamp string          \`json:"timestamp"\`
	Payload   json.RawMessage \`json:"payload"\`
}

func send(c *websocket.Conn, typ string, payload any) {
	body, _ := json.Marshal(payload)
	err := c.WriteJSON(Envelope{
		MsgID: uuid.NewString(), Type: typ, Flags: []string{},
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano), Payload: body,
	})
	if err != nil {
		log.Fatal(err)
	}
}

func main() {
	token := os.Getenv("MOTITA_TOKEN") // ${c.token}
	header := http.Header{"Authorization": {"Bearer " + token}}
	c, _, err := websocket.DefaultDialer.Dial("${WS_URL}", header)
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()

	send(c, "auth", map[string]string{"token": token}) // ${c.welcome}
	for {
		var msg Envelope
		if err := c.ReadJSON(&msg); err != nil {
			log.Fatal(err)
		}
		has := func(flag string) bool { return slices.Contains(msg.Flags, flag) }
		switch {
		case msg.Type == "auth_response" && has("AUTHENTICATED"):
			send(c, "query", map[string]string{"query": "${QUESTION}"}) // ${c.ask}
		case msg.Type == "heartbeat":
			send(c, "heartbeat_ack", map[string]string{}) // ${c.beat}
		case has("COMPLETED"):
			fmt.Println(string(msg.Payload)) // ${c.done}
			return
		case msg.Type == "error" || has("BUSY") || has("UNAUTHORIZED"):
			log.Fatalf("%s", msg.Payload) // ${c.fail}
		}
	}
}`;
}

function wsRust(c) {
  return `// Cargo.toml: tungstenite = "0.26", serde_json = "1", uuid = { version = "1", features = ["v4"] }, chrono = "0.4"
use serde_json::{json, Value};
use tungstenite::{client::IntoClientRequest, connect, Message};

fn envelope(kind: &str, payload: Value) -> Message {
    Message::text(
        json!({
            "msg_id": uuid::Uuid::new_v4().to_string(), "type": kind, "flags": [],
            "timestamp": chrono::Utc::now().to_rfc3339(), "payload": payload,
        })
        .to_string(),
    )
}

fn main() -> Result<(), Box<dyn std::error::Error>> {
    let token = std::env::var("MOTITA_TOKEN")?; // ${c.token}
    let mut request = "${WS_URL}".into_client_request()?;
    request.headers_mut().insert("Authorization", format!("Bearer {token}").parse()?);
    let (mut socket, _) = connect(request)?;

    socket.send(envelope("auth", json!({ "token": token })))?; // ${c.welcome}
    loop {
        let text = socket.read()?.into_text()?;
        let msg: Value = serde_json::from_str(text.as_str())?;
        let has = |flag: &str| msg["flags"].as_array().is_some_and(|f| f.iter().any(|x| x == flag));

        if msg["type"] == "auth_response" && has("AUTHENTICATED") {
            socket.send(envelope("query", json!({ "query": "${QUESTION}" })))?; // ${c.ask}
        } else if msg["type"] == "heartbeat" {
            socket.send(envelope("heartbeat_ack", json!({})))?; // ${c.beat}
        } else if has("COMPLETED") {
            println!("{}", msg["payload"]["result"]); // ${c.done}
            return Ok(());
        } else if msg["type"] == "error" || has("BUSY") || has("UNAUTHORIZED") {
            return Err(msg["payload"].to_string().into()); // ${c.fail}
        }
    }
}`;
}

export function wsExamples(lang) {
  const c = comments[lang];
  return [
    ["js", wsJs(c)],
    ["ts", wsTs(c)],
    ["py", wsPy(c)],
    ["go", wsGo(c)],
    ["rust", wsRust(c)],
  ];
}

const SCHEDULES = "http://127.0.0.1:7477/v1/schedules";

function schedSh(c) {
  return `# ${c.create}
curl -s -X POST "${SCHEDULES}" \\
  -H "Authorization: Bearer $MOTITA_TOKEN" \\
  -H "Content-Type: application/json" \\
  -d '{"title":"nightly audit","task":"check the logs and report","kind":"plan","every":"24h"}'`;
}

function schedJs(c) {
  return `const res = await fetch("${SCHEDULES}", {
  method: "POST",
  headers: {
    Authorization: "Bearer " + process.env.MOTITA_TOKEN, // ${c.token}
    "Content-Type": "application/json",
  },
  body: JSON.stringify({
    title: "nightly audit",
    task: "check the logs and report",
    kind: "plan", // ${c.create}
    every: "24h",
  }),
});
console.log(await res.json()); // ${c.show}`;
}

function schedTs(c) {
  return `import { env } from "node:process";

interface Schedule {
  title: string;
  task: string;
  kind: "plan" | "task";
  every: string;
}

const schedule: Schedule = {
  title: "nightly audit",
  task: "check the logs and report",
  kind: "plan", // ${c.create}
  every: "24h",
};

const res = await fetch("${SCHEDULES}", {
  method: "POST",
  headers: {
    Authorization: "Bearer " + env.MOTITA_TOKEN, // ${c.token}
    "Content-Type": "application/json",
  },
  body: JSON.stringify(schedule),
});
console.log(await res.json()); // ${c.show}`;
}

function schedPy(c) {
  return `# pip install requests
import os

import requests

res = requests.post(
    "${SCHEDULES}",
    headers={"Authorization": "Bearer " + os.environ["MOTITA_TOKEN"]},  # ${c.token}
    json={
        "title": "nightly audit",
        "task": "check the logs and report",
        "kind": "plan",  # ${c.create}
        "every": "24h",
    },
)
print(res.json())  # ${c.show}`;
}

function schedGo(c) {
  return `package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
)

func main() {
	body := strings.NewReader(
		\`{"title":"nightly audit","task":"check the logs and report","kind":"plan","every":"24h"}\`,
	) // ${c.create}
	req, err := http.NewRequest(http.MethodPost, "${SCHEDULES}", body)
	if err != nil {
		log.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+os.Getenv("MOTITA_TOKEN")) // ${c.token}
	req.Header.Set("Content-Type", "application/json")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	defer res.Body.Close()
	out, _ := io.ReadAll(res.Body)
	fmt.Println(string(out)) // ${c.show}
}`;
}

function schedRust(c) {
  return `// Cargo.toml: reqwest = { version = "0.12", features = ["blocking", "json"] }, serde_json = "1"
use serde_json::{json, Value};

fn main() -> Result<(), Box<dyn std::error::Error>> {
    let token = std::env::var("MOTITA_TOKEN")?; // ${c.token}
    let created: Value = reqwest::blocking::Client::new()
        .post("${SCHEDULES}")
        .bearer_auth(token)
        .json(&json!({
            "title": "nightly audit",
            "task": "check the logs and report",
            "kind": "plan", // ${c.create}
            "every": "24h",
        }))
        .send()?
        .json()?;
    println!("{created}"); // ${c.show}
    Ok(())
}`;
}

export function scheduleExamples(lang) {
  const c = comments[lang];
  return [
    ["sh", schedSh(c)],
    ["js", schedJs(c)],
    ["ts", schedTs(c)],
    ["py", schedPy(c)],
    ["go", schedGo(c)],
    ["rust", schedRust(c)],
  ];
}
