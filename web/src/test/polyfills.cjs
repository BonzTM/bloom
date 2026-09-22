const { Blob, File } = require("node:buffer");
const {
  ReadableStream,
  TransformStream,
  WritableStream,
} = require("node:stream/web");
const { TextDecoder, TextEncoder } = require("node:util");
const {
  BroadcastChannel: NodeBroadcastChannel,
  MessageChannel: NodeMessageChannel,
  MessagePort,
} = require("node:worker_threads");

class BroadcastChannel extends NodeBroadcastChannel {
  constructor(name) {
    super(name);
    this.unref();
  }
}

class MessageChannel extends NodeMessageChannel {
  constructor() {
    super();
    this.port1.unref();
    this.port2.unref();
  }
}

Object.assign(globalThis, {
  Blob,
  BroadcastChannel,
  File,
  MessageChannel,
  MessagePort,
  ReadableStream,
  TextDecoder,
  TextEncoder,
  TransformStream,
  WritableStream,
});

const { fetch, Headers, Request, Response } = require("undici");

Object.assign(globalThis, { fetch, Headers, Request, Response });

// React's scheduler otherwise retains Node's MessagePort in Jest workers.
delete globalThis.MessageChannel;
