/// <reference lib="webworker" />

// What the local models' workers share (speechModel.worker.ts, which speaks, and
// speechListen.worker.ts, which listens): their files, read from the page's copy or the gateway,
// and onnxruntime on the processor or on the graphics card.
import { env, InferenceSession, Tensor } from "onnxruntime-web/wasm";
import { choose } from "./choose";
import { CPU_ONLY } from "./gpuPolicy";
import { NO_WEBGPU, VOICE_CACHE, readAll, type OrtLike } from "./localVoiceCore";

declare const self: DedicatedWorkerGlobalScope;

// bytes is a downloaded file, from the page's copy when the gateway still has the same one. The
// copy outlives updates of the page, so it is asked for again with its tag: the gateway answers
// 304 while its catalog pins the same file, and the new bytes once it pins another (a newer
// onnxruntime under the same name). Without the gateway, or without the file there, the copy is
// read as it is.
export async function bytes(url: string, progress?: (loaded: number, total: number) => void): Promise<ArrayBuffer> {
  const cache = await caches.open(VOICE_CACHE);
  const hit = await cache.match(url);
  const tag = hit?.headers.get("ETag");
  const res = await fetch(url, {
    credentials: "same-origin",
    headers: choose(!!tag, { "If-None-Match": tag ?? "" }, {}),
  }).catch((err: unknown) => {
    if (!hit) {
      throw err;
    }
  });
  if (hit && !res?.ok) {
    return readAll(hit, progress);
  }
  const got = res as Response;
  if (!got.ok) {
    throw new Error("HTTP " + got.status + " " + url);
  }
  // A copy the browser cannot keep (out of room for a large model) is read from the gateway
  // again next time; the voice still loads.
  const [, body] = await Promise.all([cache.put(url, got.clone()).catch(() => undefined), readAll(got, progress)]);
  return body;
}

// external hands onnxruntime a model's weights kept apart from it, under the name the model gives.
function external(weights?: { path: string; data: ArrayBuffer }) {
  if (!weights) {
    return {};
  }
  return { externalData: [{ path: weights.path, data: new Uint8Array(weights.data) }] };
}

// The limits onnxruntime raises to the card's own when it makes its device (larger models need
// more than the defaults), and the features it turns on when the card has them.
const DEVICE_LIMITS: (keyof GPUSupportedLimits)[] = [
  "maxBindGroups",
  "maxComputeWorkgroupStorageSize",
  "maxComputeWorkgroupsPerDimension",
  "maxStorageBufferBindingSize",
  "maxBufferSize",
  "maxComputeInvocationsPerWorkgroup",
  "maxComputeWorkgroupSizeX",
  "maxComputeWorkgroupSizeY",
  "maxComputeWorkgroupSizeZ",
];
const DEVICE_FEATURES: GPUFeatureName[] = ["shader-f16", "subgroups"];
// Timing inside passes when the browser offers it, plain timestamps otherwise.
const TIMESTAMP_FEATURES = ["chromium-experimental-timestamp-query-inside-passes", "timestamp-query"] as GPUFeatureName[];

// cardDevice makes the GPU device onnxruntime runs on, on the card given.
function cardDevice(card: GPUAdapter): Promise<GPUDevice> {
  const requiredLimits: Record<string, number> = {};
  for (const name of DEVICE_LIMITS) {
    requiredLimits[name] = card.limits[name] as number;
  }
  const requiredFeatures = DEVICE_FEATURES.filter((f) => card.features.has(f));
  const timestamps = TIMESTAMP_FEATURES.find((f) => card.features.has(f));
  if (timestamps) {
    requiredFeatures.push(timestamps);
  }
  return card.requestDevice({ requiredLimits, requiredFeatures });
}

// cpuOrt is onnxruntime on the processor, given its WebAssembly.
export async function cpuOrt(wasm: ArrayBuffer): Promise<OrtLike> {
  // One thread: several need a cross-origin isolated page, which this one is not.
  env.wasm.numThreads = 1;
  env.wasm.proxy = false;
  env.wasm.wasmBinary = wasm;
  return {
    Tensor: Tensor as unknown as OrtLike["Tensor"],
    createSession: (model, weights) => InferenceSession.create(model, { executionProviders: ["wasm"], ...external(weights) }),
  } as OrtLike;
}

type GPU = { requestAdapter(o?: { powerPreference?: GPUPowerPreference }): Promise<GPUAdapter | null> };

// workerGPU is the worker's WebGPU, undefined in a browser that has none.
function workerGPU(): GPU | undefined {
  // The page names a worker CPU_ONLY where WebGPU is not allowed (a phone).
  if (self.name === CPU_ONLY) {
    return undefined;
  }
  return (self.navigator as Navigator & { gpu?: GPU }).gpu;
}

// webGPU says whether this worker has WebGPU at all: where it does not, onnxruntime's WebGPU
// build (27 MB) is not fetched.
export function webGPU(): boolean {
  return workerGPU() !== undefined;
}

// gpuOrt is onnxruntime's WebGPU build, given its WebAssembly: its sessions run on the graphics
// card. Each way it can fail says so, and the page shows it.
export async function gpuOrt(wasm: ArrayBuffer): Promise<OrtLike> {
  const gpu = workerGPU();
  if (!gpu) {
    throw new Error(NO_WEBGPU);
  }
  // The fast card first, as a laptop with two would otherwise give the slow one; then any.
  const adapter = (await gpu.requestAdapter({ powerPreference: "high-performance" })) ?? (await gpu.requestAdapter());
  if (!adapter) {
    throw new Error("the browser gave no graphics card to use");
  }
  const ortGPU = await import("onnxruntime-web/webgpu");
  ortGPU.env.wasm.numThreads = 1;
  ortGPU.env.wasm.proxy = false;
  ortGPU.env.wasm.wasmBinary = wasm;
  // onnxruntime uses the card found here, not one it would ask for again: it is handed a device
  // made on that card, as onnxruntime itself would make it.
  const device = await cardDevice(adapter);
  // Its notes on which nodes stay on the CPU are expected, not errors.
  ortGPU.env.logLevel = "error";
  return {
    Tensor: ortGPU.Tensor as unknown as OrtLike["Tensor"],
    createSession: (model, weights) =>
      ortGPU.InferenceSession.create(model, {
        executionProviders: [{ name: "webgpu", device }],
        ...external(weights),
      }),
  } as OrtLike;
}

// messageOf is what failed, as the page shows it.
export function messageOf(err: unknown): string {
  if (err instanceof Error) {
    return err.message;
  }
  return String(err);
}
