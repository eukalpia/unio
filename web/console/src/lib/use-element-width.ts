import { useLayoutEffect, useRef, useState } from "react";

/**
 * Tracks the rendered width of an element, so SVG charts can lay out in pixels
 * that match the container. The fallback is used before the first measurement,
 * and in environments without ResizeObserver.
 */
export function useElementWidth<T extends HTMLElement>(fallback: number) {
  const ref = useRef<T>(null);
  const [width, setWidth] = useState(fallback);

  useLayoutEffect(() => {
    const element = ref.current;
    if (!element) {
      return;
    }
    const measure = (): void => {
      const next = element.clientWidth;
      setWidth(next > 0 ? next : fallback);
    };
    measure();
    if (typeof ResizeObserver === "undefined") {
      return;
    }
    const observer = new ResizeObserver(measure);
    observer.observe(element);
    return () => {
      observer.disconnect();
    };
  }, [fallback]);

  return { ref, width };
}
