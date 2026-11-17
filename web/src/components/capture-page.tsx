import { useRef } from "react";
import { ArrowLeft, Inbox } from "lucide-react";
import { CaptureComposer } from "./capture-composer";

interface CapturePageProps {
  onBack: () => void;
  text: string;
  onTextChange: (text: string) => void;
}

export function CapturePage({ onBack, text, onTextChange }: CapturePageProps) {
  const inputRef = useRef<HTMLTextAreaElement>(null);
  return <main className="capture-page" onClick={(event) => {
    if (event.target instanceof HTMLElement && !event.target.closest("button, input, textarea, select, a, form") && !window.getSelection()?.toString()) inputRef.current?.focus();
  }}>
    <header className="capture-header"><button className="back-button" type="button" onClick={onBack}><ArrowLeft size={18} aria-hidden />看板</button><span className="capture-wordmark">Tack</span></header>
    <section className="capture-intro"><Inbox size={26} aria-hidden /><h1>先记下来</h1><p>想法、待办或一段笔记，之后再整理。</p></section>
    <div className="capture-page-composer"><CaptureComposer text={text} onTextChange={onTextChange} inputRef={inputRef} autoFocus /></div>
  </main>;
}
