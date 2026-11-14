import { describe, expect, it } from 'vitest';
import { SSEParser } from './sse';

describe('SSEParser', () => {
  it('parses complete messages and keeps partial ones for the next chunk', () => {
    const p = new SSEParser();
    expect(p.push('id: 1\nevent: decision\ndata: {"seq":1}\n\nid: 2\nev')).toEqual([{ id: '1', event: 'decision', data: '{"seq":1}' }]);
    expect(p.push('ent: lockdown\ndata: {"seq":2}\n\n')).toEqual([{ id: '2', event: 'lockdown', data: '{"seq":2}' }]);
  });

  it('skips keep-alive comments and joins multi-line data', () => {
    const p = new SSEParser();
    expect(p.push(': keep-alive\n\n')).toEqual([]);
    expect(p.push('data: a\ndata: b\n\n')).toEqual([{ event: 'message', data: 'a\nb' }]);
  });

  it('accepts CRLF line endings, even split across chunks', () => {
    const p = new SSEParser();
    expect(p.push('data: x\r')).toEqual([]);
    expect(p.push('\n\r\n')).toEqual([{ event: 'message', data: 'x' }]);
  });
});
