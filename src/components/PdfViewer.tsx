import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Document, Page, pdfjs } from "react-pdf";

// Vite serves the worker as a module asset. Pointing pdf.js at the bare
// filename makes it 404 and every render fails with "Setting up fake worker
// failed"; the ?url import resolves to the hashed asset URL.
import workerSrc from "pdfjs-dist/build/pdf.worker.min.mjs?url";
pdfjs.GlobalWorkerOptions.workerSrc = workerSrc;

import { tokens } from "../lib/api";
import type { RenderablePage } from "../lib/types";
import { Button, EmptyState, Icon, Skeleton } from "./ui";
import { Alert } from "./ui";

export interface PdfViewerProps {
  /** Signed or proxied URL for the document. */
  url: string;
  /** Original filename, for the download fallback. */
  fileName: string;
  /** Page metadata from the API: one entry per physical page, breaks included. */
  pages: RenderablePage[];
  /** Fired with the PHYSICAL page number whenever the learner looks at a page. */
  onViewPage: (pageNumber: number) => void;
}

/**
 * PdfViewer renders the original document with pdf.js.
 *
 * The document is never re-rendered from extracted text: pdf.js draws the
 * real page, so images, tables, fonts and layout survive intact. The API's
 * `pages` array is used only for chapter navigation and page counting.
 */
export function PdfViewer({ url, fileName, pages, onViewPage }: PdfViewerProps) {
  // Content pages are what the learner pages through; a break is shown as
  // a chapter card, never as a blank canvas, and is excluded from the count.
  const contentPages = useMemo(
    () => pages.filter((p) => !p.is_break),
    [pages],
  );
  const breaks = useMemo(() => pages.filter((p) => p.is_break), [pages]);

  const [index, setIndex] = useState(0); // index into contentPages
  const [numPages, setNumPages] = useState(0); // total pages in the file
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [containerWidth, setContainerWidth] = useState(0);
  const [showChapters, setShowChapters] = useState(false);

  const wrapRef = useRef<HTMLDivElement | null>(null);
  // The last page reported, so a re-render does not re-POST the same page.
  const reported = useRef<number | null>(null);

  const current = contentPages[index];
  const physicalPage = current?.page_number ?? 1;

  // Reset to the first content page when the document changes.
  useEffect(() => {
    setIndex(0);
    reported.current = null;
  }, [url]);

  // Reset when the page list changes shape (block switch, re-parse).
  useEffect(() => {
    setIndex((i) => (i < contentPages.length ? i : 0));
  }, [contentPages.length]);

  // Track the container so the canvas can be scaled to fit.
  useEffect(() => {
    const el = wrapRef.current;
    if (!el) return;
    const ro = new ResizeObserver((entries) => {
      const w = entries[0]?.contentRect.width ?? 0;
      setContainerWidth(w);
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, [url]);

  // Report a page exactly once per distinct physical page. The old code
  // used an inline lambda in a useEffect dependency, which changed every
  // render and re-fired the endpoint continuously.
  useEffect(() => {
    if (!current) return;
    if (reported.current === physicalPage) return;
    reported.current = physicalPage;
    onViewPage(physicalPage);
    // onViewPage is intentionally excluded: callers pass an inline lambda,
    // and including it would defeat the "report once" guard above.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [physicalPage, current]);

  const goto = useCallback(
    (next: number) => {
      setIndex(Math.max(0, Math.min(next, contentPages.length - 1)));
    },
    [contentPages.length],
  );

  const goPrev = () => setIndex((i) => Math.max(0, i - 1));
  const goNext = () => setIndex((i) => Math.min(contentPages.length - 1, i + 1));

  // Chapter jump list, built from the content pages' titles in order.
  const chapters = useMemo(() => {
    const out: { title: string; pageNumber: number; contentIndex: number }[] = [];
    const seen = new Set<string>();
    contentPages.forEach((p, i) => {
      const t = p.chapter_title;
      if (!t) return;
      const key = t.trim().toLowerCase();
      if (seen.has(key)) return;
      seen.add(key);
      out.push({ title: t, pageNumber: p.page_number, contentIndex: i });
    });
    return out;
  }, [contentPages]);

  // A signed URL needs no auth header; a proxied /v1/files URL does.
  const file = useMemo(
    () => ({
      url,
      // Supabase pre-signed URLs are bearer-free by design. Sending the
      // access token to a third-party host would leak it, so only attach
      // the header when the URL points back at this API.
      httpHeaders: url.includes("/v1/files/") && tokens.access
        ? { Authorization: `Bearer ${tokens.access}` }
        : undefined,
    }),
    [url],
  );

  if (error) {
    return (
      <div>
        <Alert tone="error">{error}</Alert>
        <p className="small muted" style={{ margin: "var(--s3) 0" }}>
          The document could not be displayed, but you can still open the
          original file.
        </p>
        <a className="btn btn-secondary" href={url} target="_blank" rel="noreferrer">
          <Icon name="download" size={15} /> Open {fileName}
        </a>
      </div>
    );
  }

  if (!url) {
    return <EmptyState icon="book" title="No document attached" />;
  }

  return (
    <div>
      <div ref={wrapRef} className="pdf-frame" style={{ width: "100%" }}>
        {loading && <Skeleton height={480} />}

        <Document
          file={file}
          loading={<Skeleton height={480} />}
          onLoadSuccess={(doc) => {
            setNumPages(doc.numPages);
            setLoading(false);
            setError(null);
          }}
          onLoadError={(err) => {
            setLoading(false);
            setError(
              err?.message
                ? `This document could not be opened: ${err.message}`
                : "This document could not be opened.",
            );
          }}
          options={{ withCredentials: false }}
        >
          {/* Only the current page is mounted, so a 200-page PDF does not
              render 200 canvases. */}
          {current && !loading && (
            <Page
              pageNumber={physicalPage}
              width={containerWidth > 0 ? containerWidth : undefined}
              renderAnnotationLayer={false}
              renderTextLayer={false}
              loading={<Skeleton height={480} />}
            />
          )}
        </Document>
      </div>

      {/* Chapter card for a break page is not rendered inline; the learner
          steps through content and the break pages are the boundaries. The
          jump list below exposes them by title. */}

      <div className="row-between" style={{ marginTop: "var(--s3)" }}>
        <Button
          size="sm"
          variant="secondary"
          disabled={index <= 0}
          onClick={goPrev}
          icon={<Icon name="chevronLeft" size={14} />}
        >
          Previous page
        </Button>
        <span className="small muted tabular">
          Page {contentPages.length === 0 ? 0 : index + 1} of {contentPages.length}
          {current?.chapter_title ? ` - ${current.chapter_title}` : ""}
          {numPages > 0 && numPages !== contentPages.length ? (
            <span className="muted"> (of {numPages} in file)</span>
          ) : null}
        </span>
        <Button
          size="sm"
          variant="secondary"
          disabled={index >= contentPages.length - 1}
          onClick={goNext}
        >
          Next page
        </Button>
      </div>

      {chapters.length > 1 && (
        <div style={{ marginTop: "var(--s3)" }}>
          <Button
            size="sm"
            variant="ghost"
            onClick={() => setShowChapters((v) => !v)}
            icon={<Icon name={showChapters ? "chevronDown" : "chevronRight"} size={14} />}
          >
            Chapters ({chapters.length})
          </Button>
          {showChapters && (
            <ul className="pdf-chapters" style={{ marginTop: "var(--s2)" }}>
              {chapters.map((c) => (
                <li key={`${c.pageNumber}-${c.title}`}>
                  <button
                    type="button"
                    className="pdf-chapter"
                    onClick={() => goto(c.contentIndex)}
                  >
                    <span>{c.title}</span>
                    <span className="muted tabular">p{c.pageNumber}</span>
                  </button>
                </li>
              ))}
            </ul>
          )}
        </div>
      )}

      {breaks.length > 0 && (
        <p className="small muted" style={{ marginTop: "var(--s3)" }}>
          {breaks.length} chapter {breaks.length === 1 ? "divider" : "dividers"} in this
          document are shown as chapter headings rather than blank pages.
        </p>
      )}
    </div>
  );
}
