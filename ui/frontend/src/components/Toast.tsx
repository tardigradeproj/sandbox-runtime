import { useEffect, useState } from "react";

type ToastEvent = { message: string; isError: boolean; id: number };
type Listener = (e: ToastEvent) => void;

let nextId = 0;
const listeners = new Set<Listener>();

// toast is called directly from anywhere (modals, views) the same way
// the mockup's global toast() function was -- no provider/context
// wiring needed at call sites, just <Toast/> mounted once in App.
export function toast(message: string, isError = false) {
  const event = { message, isError, id: nextId++ };
  for (const l of listeners) l(event);
}

export function Toast() {
  const [current, setCurrent] = useState<ToastEvent | null>(null);

  useEffect(() => {
    const listener: Listener = (e) => setCurrent(e);
    listeners.add(listener);
    return () => {
      listeners.delete(listener);
    };
  }, []);

  useEffect(() => {
    if (!current) return;
    const t = setTimeout(() => setCurrent(null), 2600);
    return () => clearTimeout(t);
  }, [current]);

  return (
    <div className={`toast${current ? " show" : ""}${current?.isError ? " toast-error" : ""}`}>
      {current?.message}
    </div>
  );
}
