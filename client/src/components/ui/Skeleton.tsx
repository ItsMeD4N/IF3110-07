export function SkeletonLine({ className = 'h-4 w-full' }: { className?: string }) {
  return (
    <div
      aria-hidden="true"
      className={`animate-pulse rounded bg-surface-subtle ${className}`}
    />
  );
}

export function CardGridSkeleton({ count = 3 }: { count?: number }) {
  return (
    <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3" role="status" aria-label="Memuat data">
      {Array.from({ length: count }).map((_, i) => (
        <div key={i} className="card-surface space-y-3 p-4">
          <div className="flex items-center justify-between gap-3">
            <SkeletonLine className="h-5 w-36" />
            <SkeletonLine className="h-5 w-20" />
          </div>
          <SkeletonLine className="h-3.5 w-24" />
          <SkeletonLine className="h-4 w-full" />
          <SkeletonLine className="h-9 w-full rounded-md" />
        </div>
      ))}
    </div>
  );
}
