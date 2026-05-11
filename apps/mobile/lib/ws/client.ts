// Phase 7.1 — singleton WebSocket client.
//
// One persistent connection to `${WS_BASE}/v1/ws`. Auth is the
// session cookie: the upgrade is a plain HTTP request and the
// platform networking stack (browser / RN okhttp / NSURLSession)
// attaches the same cookie jar `apiFetch` uses, so there's no
// token to pass.
//
// This module owns connection LIFECYCLE-MECHANICS only — opening,
// closing, exponential-backoff reconnect, and surfacing the
// current status. It does NOT decide *when* to be connected
// (that's `lib/ws/lifecycle.ts`, Phase 7.2 — connect on foreground
// + auth, disconnect on long background / logout) and it does NOT
// interpret messages (that's `lib/ws/dispatcher.ts`, Phase 7.3 —
// it subscribes via `onWSMessage`). Keeping those layers separate
// means the reconnect logic has exactly one home.
//
// Backoff: 1s, 2s, 4s, 8s, 16s, 30s (cap), reset to 1s on every
// successful open. Matches WAKEUPEXPO §4.4.
import { WS_BASE_URL } from '@/lib/env';

export type WSConnectionState = 'connected' | 'reconnecting' | 'disconnected';

// Server → client envelope shape (§7.2). `data` stays `unknown` —
// the dispatcher narrows per `type`.
export type WSEnvelope = { type: string; data?: unknown };

type StateListener = (state: WSConnectionState) => void;
type MessageListener = (envelope: WSEnvelope) => void;

const WS_URL = `${WS_BASE_URL}/v1/ws`;
const BACKOFF_BASE_MS = 1_000;
const BACKOFF_CAP_MS = 30_000;
// Normal-closure code — used when the client deliberately closes.
const NORMAL_CLOSURE = 1000;

let socket: WebSocket | null = null;
let state: WSConnectionState = 'disconnected';
// Backoff attempt counter — incremented per scheduled reconnect,
// reset to 0 on a successful open.
let reconnectAttempt = 0;
let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
// True between connect() and disconnect(). An unexpected socket
// close only schedules a reconnect while this is true; a
// deliberate disconnect() flips it false first so the close
// handler doesn't fight the teardown.
let wantConnection = false;

const stateListeners = new Set<StateListener>();
const messageListeners = new Set<MessageListener>();

function setState(next: WSConnectionState): void {
  if (state === next) return;
  state = next;
  for (const listener of stateListeners) listener(next);
}

function clearReconnectTimer(): void {
  if (reconnectTimer) {
    clearTimeout(reconnectTimer);
    reconnectTimer = null;
  }
}

// Detach every handler and close, so a stale socket can't deliver
// events or fire a reconnect after we've moved on.
function teardownSocket(ws: WebSocket | null): void {
  if (!ws) return;
  ws.onopen = null;
  ws.onmessage = null;
  ws.onerror = null;
  ws.onclose = null;
  try {
    ws.close(NORMAL_CLOSURE);
  } catch {
    // close() can throw if the socket is in CONNECTING — safe to ignore.
  }
}

function scheduleReconnect(): void {
  if (!wantConnection) return;
  clearReconnectTimer();
  // 1s, 2s, 4s, … capped at 30s.
  const delay = Math.min(BACKOFF_BASE_MS * 2 ** reconnectAttempt, BACKOFF_CAP_MS);
  reconnectAttempt += 1;
  setState('reconnecting');
  reconnectTimer = setTimeout(() => {
    reconnectTimer = null;
    openSocket();
  }, delay);
}

function openSocket(): void {
  if (!wantConnection) return;
  // Drop any lingering socket before opening a fresh one.
  teardownSocket(socket);
  socket = null;

  const ws = new WebSocket(WS_URL);
  socket = ws;

  ws.onopen = () => {
    if (socket !== ws) return; // superseded by a newer socket
    reconnectAttempt = 0;
    setState('connected');
  };

  ws.onmessage = (event: WebSocketMessageEvent) => {
    if (socket !== ws) return;
    if (typeof event.data !== 'string') return;
    let envelope: WSEnvelope | null = null;
    try {
      const parsed: unknown = JSON.parse(event.data);
      if (
        parsed &&
        typeof parsed === 'object' &&
        typeof (parsed as { type?: unknown }).type === 'string'
      ) {
        envelope = parsed as WSEnvelope;
      }
    } catch {
      // Malformed frame — ignore. The server only sends JSON
      // envelopes; anything else is noise.
    }
    if (envelope) {
      for (const listener of messageListeners) listener(envelope);
    }
  };

  ws.onerror = () => {
    // No-op — `onclose` always follows and owns the reconnect
    // decision. RN fires onerror without much detail; logging it
    // would just be noise during normal reconnect churn.
  };

  ws.onclose = () => {
    if (socket !== ws) return;
    socket = null;
    if (wantConnection) {
      scheduleReconnect();
    } else {
      setState('disconnected');
    }
  };
}

// Open the connection (idempotent). Called by the lifecycle layer
// when the app is foreground + authenticated.
export function connectWS(): void {
  wantConnection = true;
  if (
    socket &&
    (socket.readyState === WebSocket.OPEN || socket.readyState === WebSocket.CONNECTING)
  ) {
    return;
  }
  reconnectAttempt = 0;
  clearReconnectTimer();
  openSocket();
}

// Close the connection and stop reconnecting. Called on logout and
// on long background.
export function disconnectWS(): void {
  wantConnection = false;
  clearReconnectTimer();
  reconnectAttempt = 0;
  const ws = socket;
  socket = null;
  teardownSocket(ws);
  setState('disconnected');
}

// Send a client → client envelope (heartbeat / typing / presence.set).
// Returns false when the socket isn't OPEN — callers that need
// at-least-once delivery should re-send after `onWSStateChange`
// reports 'connected'.
export function sendWS(envelope: WSEnvelope): boolean {
  if (socket && socket.readyState === WebSocket.OPEN) {
    socket.send(JSON.stringify(envelope));
    return true;
  }
  return false;
}

export function getWSState(): WSConnectionState {
  return state;
}

// Subscribe to connection-state changes. Returns an unsubscribe fn
// (shape matches `useSyncExternalStore`'s `subscribe`).
export function onWSStateChange(listener: StateListener): () => void {
  stateListeners.add(listener);
  return () => {
    stateListeners.delete(listener);
  };
}

// Subscribe to inbound envelopes. The dispatcher (Phase 7.3) is the
// primary consumer; returns an unsubscribe fn.
export function onWSMessage(listener: MessageListener): () => void {
  messageListeners.add(listener);
  return () => {
    messageListeners.delete(listener);
  };
}
