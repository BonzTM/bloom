import { useEffect } from "react";

const SITE_NAME = "Bloom";

export function pageTitle(section: string): string {
  return `${section} | ${SITE_NAME}`;
}

export function usePageTitle(title: string): void {
  useEffect(() => {
    const previous = document.title;
    document.title = title;
    return () => {
      document.title = previous;
    };
  }, [title]);
}
