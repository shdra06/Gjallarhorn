import type { RecEvent } from './types';

/** One Server-Sent Event. */
export interface SSEMessage {
  id?: string;
  event: string;
  data: string;
}

/**
 * Incremental Server-Sent Events parser. Feed it text chunks as they arrive; it returns the
 * complete messages so far and keeps any partial message for the next chunk. Comments (lines
 * starting with ":") are keep-alives and are skipped.
 */
export class SSEParser {
  private buf = '';

  push(chunk: string): SSEMessage[] {
    this.buf += chunk.replace(/\r\n?/g, '\n');
    const out: SSEMessage[] = [];
    let idx: number;
    while ((idx = this.buf.indexOf('\n\n')) >= 0) {
      const block = this.buf.slice(0, idx);
      this.buf = this.buf.slice(idx + 2);
      const msg: SSEMessage = { event: 'message', data: '' };
      const data: string[] = [];
      let any = false;
      for (const line of block.split('\n')) {
        if (line === '' || line.startsWith(':')) continue;
        const colon = line.indexOf(':');
        const field = colon < 0 ? line : line.slice(0, colon);
        let value = colon < 0 ? '' : line.slice(colon + 1);
        if (value.startsWith(' ')) value = value.slice(1);
        any = true;
        if (field === 'data') data.push(value);
        else if (field === 'event') msg.event = value;
        else if (field === 'id') msg.id = value;
      }
      if (!any) continue;
      msg.data = data.join('\n');
      out.push(msg);
    }
    return out;
  }
}

export interface StreamOptions {
  token: string;
  base?: string;
  /** Sequence number to resume after. */
  after: () => number;
  onEvent: (e: RecEvent) => void;
  onStatus: (s: 'connecting' | 'live' | 'offline' | 'unauthorized') => void;
}

/**
 * Follows /v1/stream with fetch, not EventSource, so the admin token can go in a header and
 * never appears in a URL. Reconnects with backoff, resuming after the last event seen.
 * Returns a function that stops it.
 */
export function followStream(opts: StreamOptions): () => void {
  let stopped = false;
  let ctrl: AbortController | null = null;
  let delay = 500;

  const run = async () => {
    while (!stopped) {
      opts.onStatus('connecting');
      ctrl = new AbortController();
      try {
        const res = await fetch(`${opts.base ?? ''}/v1/stream?after=${opts.after()}`, {
          headers: { Authorization: `Bearer ${opts.token}` },
          signal: ctrl.signal,
        });
        if (res.status === 401) {
          opts.onStatus('unauthorized');
          return;
        }
        if (!res.ok || !res.body) throw new Error(`stream ${res.status}`);
        opts.onStatus('live');
        delay = 500;
        const reader = res.body.getReader();
        const decoder = new TextDecoder();
        const parser = new SSEParser();
        for (;;) {
          const { done, value } = await reader.read();
          if (done) break;
          for (const m of parser.push(decoder.decode(value, { stream: true }))) {
            try {
              opts.onEvent(JSON.parse(m.data) as RecEvent);
            } catch {
              // A malformed message is skipped; the next one carries on.
            }
          }
        }
      } catch {
        if (stopped) return;
      }
      if (stopped) return;
      opts.onStatus('offline');
      await new Promise((r) => setTimeout(r, delay));
      delay = Math.min(delay * 2, 8000);
    }
  };
  void run();
  return () => {
    stopped = true;
    ctrl?.abort();
  };
}
