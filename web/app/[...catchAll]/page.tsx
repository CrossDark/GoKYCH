"use client";

import { useParams, useRouter } from "next/navigation";
import { Suspense } from "react";
import DiscussionListPage from "../discuss/page";

export default function CatchAllPage() {
  const params = useParams<{ catchAll: string[] }>();
  const router = useRouter();
  const decodedParts = params.catchAll?.map(decodeURIComponent) || [];

  if (decodedParts[0] === "讨论") {
    if (decodedParts.length === 1) {
      return (
        <Suspense fallback={<div>加载中...</div>}>
          <DiscussionListPage />
        </Suspense>
      );
    }

    if (decodedParts.length === 2) {
      router.replace(`/discuss/${decodedParts[1]}`);
      return <div>重定向中...</div>;
    }
  }

  router.replace("/");
  return <div>重定向中...</div>;
}