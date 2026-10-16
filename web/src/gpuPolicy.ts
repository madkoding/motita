// WHERE THE LOCAL VOICES MAY USE WebGPU. A phone's graphics card is too weak for them (speech input
// and the voices both slow down or run the device out of memory), so there they run on the
// processor, WebAssembly, even when the browser offers WebGPU. Settings → Voice has a switch to
// force it on anyway.
import { choose } from "./choose";
import { isPhone } from "./viewport";

export const GPU_FORCED_KEY = "motita.ui.localGpuForced";

// CPU_ONLY is the name given to a worker that must not use WebGPU; the worker reads it from
// `self.name`, as it cannot ask the page's storage.
export const CPU_ONLY = "cpu-only";

const MOBILE_UA = /Android|iPhone|iPad|iPod|Mobile/i;

// isMobileDevice says this is a phone or a tablet: a narrow screen, a touch screen without hover,
// or a mobile browser's own word for itself (in case the others are hidden, as in desktop mode).
export function isMobileDevice(): boolean {
  return isPhone() || touchOnly() || MOBILE_UA.test(navigator.userAgent);
}

function touchOnly(): boolean {
  try {
    return window.matchMedia("(pointer: coarse) and (hover: none)").matches;
  } catch {
    return false;
  }
}

// gpuForced says the reader switched WebGPU on for this mobile device.
export function gpuForced(): boolean {
  try {
    return localStorage.getItem(GPU_FORCED_KEY) === "1";
  } catch {
    return false;
  }
}

// setGpuForced keeps the choice; a browser that cannot store it forgets it.
export function setGpuForced(on: boolean): void {
  try {
    localStorage.setItem(GPU_FORCED_KEY, choose(on, "1", "0"));
  } catch {
    // Nothing to do: the choice is only a convenience.
  }
}

// gpuAllowed says the local voices may use WebGPU here: anywhere but a mobile device, unless forced.
export function gpuAllowed(): boolean {
  return !isMobileDevice() || gpuForced();
}

// workerName is the name for a new worker: CPU_ONLY where WebGPU is not allowed.
export function workerName(): string {
  return choose(gpuAllowed(), "", CPU_ONLY);
}
