import React, { useEffect, useState } from "react";
import { ChevronLeft, ChevronRight } from "lucide-react";
import { Button } from "./UI";

export const LIST_PAGE_SIZE = 5;

export function useListPage(items, filterKey) {
  const [view, setView] = useState({ key: filterKey, page: 1 });
  const pages = Math.max(1, Math.ceil(items.length / LIST_PAGE_SIZE));
  const page = view.key === filterKey ? Math.min(view.page, pages) : 1;
  useEffect(() => {
    if (view.key !== filterKey || view.page !== page)
      setView({ key: filterKey, page });
  }, [filterKey, page, view.key, view.page]);
  return {
    page,
    pages,
    items: items.slice((page - 1) * LIST_PAGE_SIZE, page * LIST_PAGE_SIZE),
    onChange: (next) =>
      setView({ key: filterKey, page: Math.max(1, Math.min(pages, next)) }),
  };
}

export default function ListPagination({
  page,
  pages,
  onChange,
  label,
  compact = false,
}) {
  if (pages <= 1) return null;
  return (
    <nav
      className={`list-pagination ${compact ? "is-compact" : ""}`}
      aria-label={label}
    >
      <Button
        icon={ChevronLeft}
        aria-label="上一页"
        disabled={page <= 1}
        onClick={() => onChange(page - 1)}
      >
        {!compact && "上一页"}
      </Button>
      <span aria-live="polite">
        第 {page} / {pages} 页
      </span>
      <Button
        icon={ChevronRight}
        aria-label="下一页"
        disabled={page >= pages}
        onClick={() => onChange(page + 1)}
      >
        {!compact && "下一页"}
      </Button>
    </nav>
  );
}
