import { lazy, useState, type ComponentType } from "react";

/**
 * Like React.lazy, but once the file has been loaded (by `preload`, or by an
 * earlier visit) the component renders straight away. React.lazy suspends on
 * first render even when the file is already there, and React then holds the
 * screen back for up to 300 ms.
 */
export function lazyScreen<P extends object, M extends { default: ComponentType<P> }>(load: () => Promise<M>) {
  let loaded: ComponentType<P> | undefined;
  let pending: Promise<M> | undefined;
  const preload = () =>
    (pending ??= load().then(
      (m) => {
        loaded = m.default;
        return m;
      },
      (err) => {
        pending = undefined;
        throw err;
      },
    ));
  const Lazy = lazy(preload) as unknown as ComponentType<P>;

  function Screen(props: P) {
    // Chosen once per mount, so a screen never remounts when its file arrives.
    const [Component] = useState(() => loaded ?? Lazy);
    return <Component {...props} />;
  }
  return Object.assign(Screen, { preload });
}
