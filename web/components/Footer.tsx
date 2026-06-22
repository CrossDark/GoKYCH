export function Footer() {
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
      </div>
    </footer>
  );
}
