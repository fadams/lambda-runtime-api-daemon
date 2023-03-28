use std::io::{self, Cursor, Read, Write};
use image::{ImageOutputFormat, ImageFormat};

fn main() {
    let mut buf = Vec::new();
    io::stdin().read_to_end(&mut buf).unwrap();

    let image_format_detected: ImageFormat = image::guess_format(&buf).unwrap();
    let img = image::load_from_memory(&buf).unwrap();
    let filtered = img.grayscale();
    // In image 0.24.6 DynamicImage write_to requires the Seek trait which
    // wasn't required in image 0.23.0 used in the original version of this
    // example, so we now use Cursor::new(vec![]) instead of just vec![]
    let mut buf = Cursor::new(vec![]);
    match image_format_detected {
        ImageFormat::Gif => {
            filtered.write_to(&mut buf, ImageOutputFormat::Gif).unwrap();
        },
        _ => {
            filtered.write_to(&mut buf, ImageOutputFormat::Png).unwrap();
        },
    };
    // into_inner consumes the cursor, returning the underlying value.
    // https://doc.rust-lang.org/std/io/struct.Cursor.html#method.into_inner
    io::stdout().write_all(&buf.into_inner()).unwrap();
    io::stdout().flush().unwrap();
}
