// Racing a read does not cancel its implementation. Its late value must remain inert.
export function abortable<T>(promise: Promise<T>, signal: AbortSignal): Promise<T> {
  return new Promise((resolve, reject) => {
    let settled = false;
    const complete = (action: () => void) => {
      if (settled) return;
      settled = true; signal.removeEventListener('abort', abort); action();
    };
    const abort = () => complete(() => reject(signal.reason));
    signal.addEventListener('abort', abort, { once: true });
    promise.then(value => complete(() => resolve(value)), error => complete(() => reject(error)));
    if (signal.aborted) abort();
  });
}
