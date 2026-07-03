import { useEffect, useState } from "react";

// 全局确认/提示弹窗：替代系统 confirm/alert（样式统一、支持危险色按钮）。
// 用法：在 App 根挂一次 <DialogHost/>，任意处 await confirmDialog("...") / alertDialog("...")。

interface DialogState {
  message: string;
  danger?: boolean;
  alertOnly?: boolean;
  confirmText?: string;
}

let show: ((s: DialogState) => Promise<boolean>) | null = null;

export function confirmDialog(message: string, opts?: { danger?: boolean; confirmText?: string }): Promise<boolean> {
  if (!show) return Promise.resolve(window.confirm(message)); // Host 未挂载的兜底
  return show({ message, danger: opts?.danger, confirmText: opts?.confirmText });
}

export function alertDialog(message: string): Promise<boolean> {
  if (!show) {
    window.alert(message);
    return Promise.resolve(true);
  }
  return show({ message, alertOnly: true });
}

export default function DialogHost() {
  const [state, setState] = useState<DialogState | null>(null);
  const [resolver, setResolver] = useState<((ok: boolean) => void) | null>(null);

  useEffect(() => {
    show = (s) =>
      new Promise<boolean>((resolve) => {
        setState(s);
        setResolver(() => resolve);
      });
    return () => {
      show = null;
    };
  }, []);

  if (!state) return null;
  const close = (ok: boolean) => {
    resolver?.(ok);
    setState(null);
    setResolver(null);
  };

  return (
    <div className="dlg-mask" onClick={() => close(false)}>
      <div className="dlg" onClick={(e) => e.stopPropagation()}>
        <div className="dlg-msg">{state.message}</div>
        <div className="dlg-actions">
          {!state.alertOnly && <button onClick={() => close(false)}>取消</button>}
          <button
            className={state.danger ? "danger" : "primary"}
            autoFocus
            onClick={() => close(true)}
          >
            {state.alertOnly ? "知道了" : state.confirmText || "确认"}
          </button>
        </div>
      </div>
    </div>
  );
}
