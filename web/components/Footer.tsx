"use client";

import { useEffect, useState } from "react";
import { getSite } from "@/lib/api";

export function Footer() {
  const [icp, setIcp] = useState<string>("");
  // ICP record number is admin-editable in /admin/settings → settings.yml →
  // /api/site. Read it here so it surfaces in the footer of every page.
  useEffect(() => {
    getSite()
      .then((d) => setIcp(d.site?.icp_number ?? ""))
      .catch(() => {});
  }, []);
  return (
    <footer className="site-footer">
      <div className="footer-inner">
        <p>
          &copy; {new Date().getFullYear()}{" "}
          <a href="https://github.com/kych404/GoKYCH" target="_blank" rel="noopener noreferrer">
            GoKYCH
          </a>
          {" — "}
          <span>Powered by Go + Next.js</span>
        </p>
        {icp && (
          <p className="site-footer-icp">
            <a
              href="https://beian.miit.gov.cn/"
              target="_blank"
              rel="noopener noreferrer"
            >
              {icp}
            </a>
          </p>
        )}
      </div>
    </footer>
  );
}
